#include "registry.h"

#include "array.h"
#include "index.h"

#include <netinet/in.h>

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

	registry->mctx = mctx;

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

ssize_t
service_registry_find_or_insert_service(
	struct service_registry *registry,
	uint8_t *vip_address,
	int vip_proto,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto,
	struct service_info **result
) {
	struct service_index *index = &registry->index;
	struct service_array *array = &registry->array;
	ssize_t idx = service_index_lookup(
		index,
		array,
		vip_address,
		vip_proto,
		ip_address,
		ip_proto,
		port,
		transport_proto
	);
	if (idx == -1) {
		int res = service_array_push_back(
			array,
			vip_address,
			vip_proto,
			ip_address,
			ip_proto,
			port,
			transport_proto
		);
		if (res != 0) {
			return -1;
		}
		idx = array->size - 1;

		// Insert the new service into the index
		res = service_index_insert(
			index,
			array,
			vip_address,
			vip_proto,
			ip_address,
			ip_proto,
			port,
			transport_proto,
			idx
		);
		if (res != 0) {
			return -1;
		}
	}

	*result = service_array_lookup(array, idx);

	return idx;
}

struct service_info *
service_registry_lookup(struct service_registry *registry, size_t idx) {
	return service_array_lookup(&registry->array, idx);
}