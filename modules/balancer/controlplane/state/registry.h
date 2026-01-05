#pragma once

#include <assert.h>
#include <netinet/in.h>
#include <stddef.h>
#include <stdint.h>

#include "array.h"
#include "index.h"
#include "service.h"

////////////////////////////////////////////////////////////////////////////////

/**
 * Registry of services (virtual services and reals).
 * Provides indexing and lookup by identifier.
 */
struct service_registry {
	struct service_array array; // Dense storage
	struct service_index index; // Mapping from identifier to array index
};

/**
 * Initialize service registry.
 * Returns 0 on success, -1 on error.
 */
int
service_registry_init(
	struct service_registry *registry, struct memory_context *mctx
);

/**
 * Free resources held by the registry.
 */
void
service_registry_free(struct service_registry *registry);

/**
 * Find a service by identifier or insert a new one if absent.
 *
 * On success returns a pointer to the service and writes its index to
 * idx_output. Returns NULL on error.
 */
union service_state *
service_registry_find_or_insert_service(
	struct service_registry *registry,
	union service_identifier *id,
	size_t *idx_output
);

/**
 * Lookup service index by identifier.
 * Returns non-negative index on success, or -1 if not found.
 */
ssize_t
service_registry_lookup_by_id(
	struct service_registry *registry, union service_identifier *id
);

/**
 * Lookup service by registry index.
 * Returns pointer to service; behavior is undefined if idx is out of bounds.
 */
union service_state *
service_registry_lookup(struct service_registry *registry, size_t idx);

/**
 * Number of services stored in the registry.
 */
size_t
service_registry_size(struct service_registry *registry);