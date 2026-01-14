#include "index.h"

#include "array.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/ttlmap/detail/city.h"
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

/// Compute hash for the service 6-tuple key
static uint32_t
registry_index_hash(union service_identifier *identifier) {
	return city_hash32(
		(const char *)identifier, sizeof(union service_identifier)
	);
}

/// Check if a service matches the given key
static inline int
service_index_matches(
	union service_identifier *a, union service_identifier *b
) {
	// Only compare the identifier portion, not any additional state fields
	return memcmp(a, b, sizeof(union service_identifier)) == 0;
}

/// Allocate a new index entry
static struct service_index_entry *
registry_index_entry_alloc(struct service_index *index, size_t service_idx) {
	struct service_index_entry *entry =
		memory_balloc(&index->mctx, sizeof(struct service_index_entry));
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
	memory_bfree(&index->mctx, entry, sizeof(struct service_index_entry));
}

static void
bucket_insert(
	struct service_index_entry *
		*bucket /*pointer to the relative pointer on the bucket head*/,
	struct service_index_entry *entry
) {
	SET_OFFSET_OF(&entry->next, ADDR_OF(bucket));
	SET_OFFSET_OF(bucket, entry);
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
		&index->mctx,
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
	struct service_index_entry **old_buckets = ADDR_OF(&index->buckets);
	for (size_t i = 0; i < index->bucket_count; ++i) {
		struct service_index_entry *entry = ADDR_OF(&old_buckets[i]);
		while (entry != NULL) {
			struct service_index_entry *next =
				ADDR_OF(&entry->next);

			// Get the service to recompute hash
			union service_identifier *service =
				service_id(service_array_lookup(
					services, entry->service_idx
				));

			// Compute new hash and bucket index
			uint32_t hash = registry_index_hash(service);
			size_t bucket_idx = hash % new_bucket_count;

			// Insert at head of new bucket
			bucket_insert(&new_buckets[bucket_idx], entry);

			entry = next;
		}
	}

	// Free old bucket array
	if (old_buckets != NULL) {
		memory_bfree(
			&index->mctx,
			old_buckets,
			sizeof(struct service_index_entry *) *
				index->bucket_count
		);
	}

	// Update index
	SET_OFFSET_OF(&index->buckets, new_buckets);
	index->bucket_count = new_bucket_count;

	return 0;
}

////////////////////////////////////////////////////////////////////////////////
// Public API implementation

int
service_index_init(struct service_index *index, struct memory_context *mctx) {
	memory_context_init_from(&index->mctx, mctx, "service_index");

	// Allocate initial bucket array
	struct service_index_entry **buckets = memory_balloc(
		mctx,
		sizeof(struct service_index_entry *) *
			REGISTRY_INDEX_INITIAL_BUCKETS
	);
	if (buckets == NULL) {
		return -1;
	};
	memset(buckets,
	       0,
	       sizeof(struct service_index_entry *) *
		       REGISTRY_INDEX_INITIAL_BUCKETS);

	SET_OFFSET_OF(&index->buckets, buckets);

	index->bucket_count = REGISTRY_INDEX_INITIAL_BUCKETS;
	index->entry_count = 0;

	return 0;
}

void
service_index_free(struct service_index *index) {
	if (index == NULL || index->buckets == NULL) {
		return;
	}

	struct service_index_entry **buckets = ADDR_OF(&index->buckets);

	// Free all entries
	for (size_t i = 0; i < index->bucket_count; ++i) {
		struct service_index_entry *entry = ADDR_OF(&buckets[i]);
		while (entry != NULL) {
			struct service_index_entry *next =
				ADDR_OF(&entry->next);
			registry_index_entry_free(index, entry);
			entry = next;
		}
	}

	// Free bucket array
	memory_bfree(
		&index->mctx,
		buckets,
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
	union service_identifier *identifier
) {
	if (index == NULL || index->buckets == NULL) {
		return -1;
	}

	// Compute hash and bucket index
	uint32_t hash = registry_index_hash(identifier);
	size_t bucket_idx = hash % index->bucket_count;

	struct service_index_entry **buckets = ADDR_OF(&index->buckets);

	// Search in the bucket's chain
	struct service_index_entry *entry = ADDR_OF(&buckets[bucket_idx]);
	while (entry != NULL) {
		// Get the service and compare ONLY the identifier portion
		union service_state *state =
			service_array_lookup(services, entry->service_idx);
		union service_identifier *service = service_id(state);

		if (service_index_matches(service, identifier)) {
			return entry->service_idx;
		}

		entry = ADDR_OF(&entry->next);
	}

	return -1;
}

int
service_index_insert(
	struct service_index *index,
	struct service_array *services,
	union service_identifier *identifier,
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
	uint32_t hash = registry_index_hash(identifier);
	size_t bucket_idx = hash % index->bucket_count;

	// Allocate new entry
	struct service_index_entry *entry =
		registry_index_entry_alloc(index, service_idx);
	if (entry == NULL) {
		return -1;
	}

	struct service_index_entry **buckets = ADDR_OF(&index->buckets);

	// Insert at head of bucket
	bucket_insert(&buckets[bucket_idx], entry);
	++index->entry_count;

	return 0;
}