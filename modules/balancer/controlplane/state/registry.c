#include "registry.h"

#include "array.h"
#include "controlplane/diag/diag.h"
#include "index.h"
#include "service.h"

#include <netinet/in.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

int
service_registry_init(
	struct service_registry *registry, struct memory_context *mctx
) {
	// initialize array
	service_array_init(&registry->array, mctx);

	// initialize the hash table index
	int res = service_index_init(&registry->index, mctx);
	if (res != 0) {
		return -1;
	}

	return 0;
}

void
service_registry_free(struct service_registry *registry) {
	// free the services array
	service_array_free(&registry->array);

	// free the hash table index
	service_index_free(&registry->index);
}

////////////////////////////////////////////////////////////////////////////////

union service_state *
service_registry_find_or_insert_service(
	struct service_registry *registry,
	union service_identifier *id,
	size_t *idx_output
) {
	struct service_index *index = &registry->index;
	struct service_array *array = &registry->array;

	ssize_t idx = service_index_lookup(index, array, id);
	if (idx == -1) {
		union service_state state;
		memset(&state, 0, sizeof(state));
		memcpy(&state, id, sizeof(union service_identifier));

		int res = service_array_push_back(array, &state);
		if (res != 0) {
			NEW_ERROR("failed to push service into array");
			return NULL;
		}
		idx = array->size - 1;

		// Insert the new service into the index
		res = service_index_insert(index, array, id, idx);
		if (res != 0) {
			NEW_ERROR("failed to insert service into index");
			return NULL;
		}
	}

	*idx_output = idx;

	return service_array_lookup(array, idx);
}

ssize_t
service_registry_lookup_by_id(
	struct service_registry *registry, union service_identifier *id
) {
	return service_index_lookup(&registry->index, &registry->array, id);
}

union service_state *
service_registry_lookup(struct service_registry *registry, size_t idx) {
	return service_array_lookup(&registry->array, idx);
}

size_t
service_registry_size(struct service_registry *registry) {
	return registry->array.size;
}