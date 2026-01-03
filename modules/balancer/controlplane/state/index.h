#pragma once

#include <netinet/in.h>
#include <stddef.h>
#include <stdint.h>
#include <sys/types.h>

#include "common/memory.h"

////////////////////////////////////////////////////////////////////////////////

// Forward declaration
union service_identifier;
struct service_array;

/// Entry in the hash table for collision chaining
struct service_index_entry {
	/// Index in service_registry->services array
	size_t service_idx;

	/// Next entry in the collision chain (NULL if last)
	struct service_index_entry *next;
};

////////////////////////////////////////////////////////////////////////////////

/// Hash table index for fast service lookup
/// Maps (vip_address, vip_proto, ip_address, ip_proto, port, transport_proto)
/// to service index in the registry
struct service_index {
	/// Array of bucket head pointers (separate chaining)
	struct service_index_entry **buckets;

	/// Current number of buckets in the hash table
	size_t bucket_count;

	/// Current number of entries in the hash table
	size_t entry_count;

	/// Memory context for allocations
	struct memory_context mctx;
};

////////////////////////////////////////////////////////////////////////////////

/// Initialize the registry index with default bucket count
/// @param index Pointer to the index structure to initialize
/// @param mctx Memory context for allocations
/// @return 0 on success, -1 on error
int
service_index_init(struct service_index *index, struct memory_context *mctx);

/// Free all memory used by the index
/// @param index Pointer to the index structure to free
void
service_index_free(struct service_index *index);

/// Lookup a service by its 6-tuple key
/// @param index Pointer to the index structure
/// @param services Array of service_info structures
/// @param vip_address Virtual IP address (4 or 16 bytes)
/// @param vip_proto Virtual IP protocol (IPPROTO_IP or IPPROTO_IPV6)
/// @param ip_address Real IP address (4 or 16 bytes)
/// @param ip_proto Real IP protocol (IPPROTO_IP or IPPROTO_IPV6)
/// @param port Port number
/// @param transport_proto Transport protocol (IPPROTO_TCP or IPPROTO_UDP)
/// @return Service index if found, -1 if not found
ssize_t
service_index_lookup(
	struct service_index *index,
	struct service_array *services,
	union service_identifier *identifier
);

/// Insert a new service index mapping
/// @param index Pointer to the index structure
/// @param services Array of service_info structures
/// @param vip_address Virtual IP address (4 or 16 bytes)
/// @param vip_proto Virtual IP protocol (IPPROTO_IP or IPPROTO_IPV6)
/// @param ip_address Real IP address (4 or 16 bytes)
/// @param ip_proto Real IP protocol (IPPROTO_IP or IPPROTO_IPV6)
/// @param port Port number
/// @param transport_proto Transport protocol (IPPROTO_TCP or IPPROTO_UDP)
/// @param service_idx Index of the service in the registry
/// @return 0 on success, -1 on error
/// @note Does not check for duplicates - caller must ensure uniqueness
int
service_index_insert(
	struct service_index *index,
	struct service_array *services,
	union service_identifier *identifier,
	size_t service_idx
);

/// Clear all entries from the index
/// @param index Pointer to the index structure
/// @note Keeps buckets allocated for reuse
void
service_index_free(struct service_index *index);