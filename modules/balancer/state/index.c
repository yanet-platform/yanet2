#include "index.h"

#include "array.h"
#include "common/network.h"
#include "service.h"

#include <netinet/in.h>
#include <stdio.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////
// Constants

/// Initial number of buckets in the hash table
#define REGISTRY_INDEX_INITIAL_BUCKETS 16

/// Load factor threshold for triggering resize (0.75 = 75%)
#define REGISTRY_INDEX_LOAD_FACTOR_NUM 3
#define REGISTRY_INDEX_LOAD_FACTOR_DEN 4

/// FNV-1a hash algorithm constants
#define FNV_OFFSET_BASIS 0xcbf29ce484222325ULL
#define FNV_PRIME 0x100000001b3ULL

////////////////////////////////////////////////////////////////////////////////
// Internal helper functions

/// Compute FNV-1a hash for a buffer
static inline uint64_t
fnv1a_hash_buffer(uint64_t hash, const uint8_t *data, size_t len) {
	for (size_t i = 0; i < len; ++i) {
		hash ^= data[i];
		hash *= FNV_PRIME;
	}
	return hash;
}

/// Compute FNV-1a hash for an integer value
static inline uint64_t
fnv1a_hash_int(uint64_t hash, uint64_t value) {
	for (size_t i = 0; i < sizeof(value); ++i) {
		hash ^= (value >> (i * 8)) & 0xFF;
		hash *= FNV_PRIME;
	}
	return hash;
}

/// Compute hash for the service 6-tuple key
static uint64_t
registry_index_hash(
	uint8_t *vip_address,
	int vip_proto,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto
) {
	uint64_t hash = FNV_OFFSET_BASIS;

	// Hash VIP address (4 or 16 bytes depending on protocol)
	size_t vip_len = (vip_proto == IPPROTO_IPV6) ? NET6_LEN : NET4_LEN;
	hash = fnv1a_hash_buffer(hash, vip_address, vip_len);

	// Mix in VIP protocol
	hash = fnv1a_hash_int(hash, vip_proto);

	// Hash real IP address (4 or 16 bytes depending on protocol)
	size_t ip_len = (ip_proto == IPPROTO_IPV6) ? NET6_LEN : NET4_LEN;
	hash = fnv1a_hash_buffer(hash, ip_address, ip_len);

	// Mix in real IP protocol
	hash = fnv1a_hash_int(hash, ip_proto);

	// Mix in port
	hash = fnv1a_hash_int(hash, port);

	// Mix in transport protocol
	hash = fnv1a_hash_int(hash, transport_proto);

	return hash;
}

/// Check if a service matches the given key
static int
service_index_matches(
	struct service_info *service,
	uint8_t *vip_address,
	int vip_proto,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto
) {
	// Check protocols first (fast comparison)
	if (service->vip_proto != vip_proto || service->ip_proto != ip_proto ||
	    service->transport_proto != transport_proto) {
		return 0;
	}

	// Check port
	if (service->port != port) {
		return 0;
	}

	// Check VIP address
	size_t vip_len = (vip_proto == IPPROTO_IPV6) ? NET6_LEN : NET4_LEN;
	if (memcmp(service->vip_address, vip_address, vip_len) != 0) {
		return 0;
	}

	// Check real IP address
	size_t ip_len = (ip_proto == IPPROTO_IPV6) ? NET6_LEN : NET4_LEN;
	if (memcmp(service->ip_address, ip_address, ip_len) != 0) {
		return 0;
	}

	return 1;
}

/// Allocate a new index entry
static struct service_index_entry *
registry_index_entry_alloc(struct service_index *index, size_t service_idx) {
	struct service_index_entry *entry =
		memory_balloc(index->mctx, sizeof(struct service_index_entry));
	if (entry == NULL) {
		return NULL;
	}

	entry->service_idx = service_idx;
	entry->next = NULL;

	return entry;
}

/// Free an index entry
static void
registry_index_entry_free(
	struct service_index *index, struct service_index_entry *entry
) {
	memory_bfree(index->mctx, entry, sizeof(struct service_index_entry));
}

/// Resize the hash table to a new bucket count
static int
registry_index_resize(
	struct service_index *index,
	struct service_array *services,
	size_t new_bucket_count
) {
	// Allocate new bucket array
	struct service_index_entry **new_buckets = memory_balloc(
		index->mctx,
		sizeof(struct service_index_entry *) * new_bucket_count
	);
	if (new_buckets == NULL) {
		return -1;
	}

	// Initialize new buckets to NULL
	memset(new_buckets,
	       0,
	       sizeof(struct service_index_entry *) * new_bucket_count);

	// Rehash all existing entries
	for (size_t i = 0; i < index->bucket_count; ++i) {
		struct service_index_entry *entry = index->buckets[i];
		while (entry != NULL) {
			struct service_index_entry *next = entry->next;

			// Get the service to recompute hash
			struct service_info *service = service_array_lookup(
				services, entry->service_idx
			);

			// Compute new hash and bucket index
			uint64_t hash = registry_index_hash(
				service->vip_address,
				service->vip_proto,
				service->ip_address,
				service->ip_proto,
				service->port,
				service->transport_proto
			);
			size_t bucket_idx = hash % new_bucket_count;

			// Insert at head of new bucket
			entry->next = new_buckets[bucket_idx];
			new_buckets[bucket_idx] = entry;

			entry = next;
		}
	}

	// Free old bucket array
	if (index->buckets != NULL) {
		memory_bfree(
			index->mctx,
			index->buckets,
			sizeof(struct service_index_entry *) *
				index->bucket_count
		);
	}

	// Update index
	index->buckets = new_buckets;
	index->bucket_count = new_bucket_count;

	return 0;
}

////////////////////////////////////////////////////////////////////////////////
// Public API implementation

int
service_index_init(struct service_index *index, struct memory_context *mctx) {
	if (index == NULL || mctx == NULL) {
		return -1;
	}

	// Allocate initial bucket array
	index->buckets = memory_balloc(
		mctx,
		sizeof(struct service_index_entry *) *
			REGISTRY_INDEX_INITIAL_BUCKETS
	);
	if (index->buckets == NULL) {
		return -1;
	}

	// Initialize buckets to NULL
	memset(index->buckets,
	       0,
	       sizeof(struct service_index_entry *) *
		       REGISTRY_INDEX_INITIAL_BUCKETS);

	index->bucket_count = REGISTRY_INDEX_INITIAL_BUCKETS;
	index->entry_count = 0;
	index->mctx = mctx;

	return 0;
}

void
service_index_free(struct service_index *index) {
	if (index == NULL || index->buckets == NULL) {
		return;
	}

	// Free all entries
	for (size_t i = 0; i < index->bucket_count; ++i) {
		struct service_index_entry *entry = index->buckets[i];
		while (entry != NULL) {
			struct service_index_entry *next = entry->next;
			registry_index_entry_free(index, entry);
			entry = next;
		}
	}

	// Free bucket array
	memory_bfree(
		index->mctx,
		index->buckets,
		sizeof(struct service_index_entry *) * index->bucket_count
	);

	index->buckets = NULL;
	index->bucket_count = 0;
	index->entry_count = 0;
}

ssize_t
service_index_lookup(
	struct service_index *index,
	struct service_array *services,
	uint8_t *vip_address,
	int vip_proto,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto
) {
	if (index == NULL || index->buckets == NULL) {
		return -1;
	}

	// Compute hash and bucket index
	uint64_t hash = registry_index_hash(
		vip_address,
		vip_proto,
		ip_address,
		ip_proto,
		port,
		transport_proto
	);
	size_t bucket_idx = hash % index->bucket_count;

	// Search in the bucket's chain
	struct service_index_entry *entry = index->buckets[bucket_idx];
	while (entry != NULL) {
		// Get the service and compare keys
		struct service_info *service =
			service_array_lookup(services, entry->service_idx);

		if (service_index_matches(
			    service,
			    vip_address,
			    vip_proto,
			    ip_address,
			    ip_proto,
			    port,
			    transport_proto
		    )) {
			return entry->service_idx;
		}

		entry = entry->next;
	}

	return -1;
}

int
service_index_insert(
	struct service_index *index,
	struct service_array *services,
	uint8_t *vip_address,
	int vip_proto,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto,
	size_t service_idx
) {
	if (index == NULL || index->buckets == NULL) {
		return -1;
	}

	// Check if resize is needed (load factor > 0.75)
	if (index->entry_count * REGISTRY_INDEX_LOAD_FACTOR_DEN >=
	    index->bucket_count * REGISTRY_INDEX_LOAD_FACTOR_NUM) {
		int res = registry_index_resize(
			index, services, index->bucket_count * 2
		);
		if (res != 0) {
			return -1;
		}
	}

	// Compute hash and bucket index
	uint64_t hash = registry_index_hash(
		vip_address,
		vip_proto,
		ip_address,
		ip_proto,
		port,
		transport_proto
	);
	size_t bucket_idx = hash % index->bucket_count;

	// Allocate new entry
	struct service_index_entry *entry =
		registry_index_entry_alloc(index, service_idx);
	if (entry == NULL) {
		return -1;
	}

	// Insert at head of bucket
	entry->next = index->buckets[bucket_idx];
	index->buckets[bucket_idx] = entry;
	++index->entry_count;

	return 0;
}