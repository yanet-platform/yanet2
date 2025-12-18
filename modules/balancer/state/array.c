#include "array.h"
#include "common/memory.h"
#include "service.c"
#include <assert.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

static inline size_t
service_array_block_count(struct service_array *array) {
	return (array->size + SERVICE_REGISTRY_BLOCK_SIZE - 1) /
	       SERVICE_REGISTRY_BLOCK_SIZE;
}

void
service_array_init(struct service_array *array, struct memory_context *mctx) {
	array->mctx = mctx;
	array->size = 0;
	array->blocks = NULL;
}

void
service_array_free(struct service_array *array) {
	size_t blocks = service_array_block_count(array);
	for (size_t i = 0; i < blocks; ++i) {
		struct service_array_block *block = array->blocks[i];
		memory_bfree(
			array->mctx, block, sizeof(struct service_array_block)
		);
	}
	memory_bfree(
		array->mctx, array->blocks, sizeof(struct service_array_block *)
	);
}

struct service_info *
service_array_lookup(struct service_array *array, size_t idx) {
	assert(idx < array->size);
	return &array->blocks[idx / SERVICE_REGISTRY_BLOCK_SIZE]
			->services[idx % SERVICE_REGISTRY_BLOCK_SIZE];
}

int
service_array_push_back(
	struct service_array *array,
	uint8_t *vip_address,
	int vip_proto,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto
) {
	if (array->size % SERVICE_REGISTRY_BLOCK_SIZE == 0) {
		// need allocate new block
		// for this, we reallocate the whole blocks array
		size_t blocks = service_array_block_count(array) + 1;
		struct service_array_block **new_blocks = memory_balloc(
			array->mctx,
			blocks * sizeof(struct service_array_block *)
		);
		if (new_blocks == NULL) {
			return -1;
		}

		// copy the old blocks
		if (blocks > 1) {
			memcpy(new_blocks,
			       array->blocks,
			       sizeof(struct service_array_block *) *
				       (blocks - 1));
		}

		// create and initialize new block
		struct service_array_block *new_block = memory_balloc(
			array->mctx, sizeof(struct service_array_block)
		);
		if (new_block == NULL) {
			memory_bfree(
				array->mctx,
				new_blocks,
				sizeof(struct service_array_block *) * blocks
			);
			return -1;
		}

		memset(new_block, 0, sizeof(struct service_array_block));

		new_blocks[blocks - 1] = new_block;

		memory_bfree(
			array->mctx,
			array->blocks,
			sizeof(struct service_array_block *) * (blocks - 1)
		);

		array->blocks = new_blocks;
	}

	// initialize service
	array->size++;

	struct service_info *service =
		service_array_lookup(array, array->size - 1);
	service_info_init(
		service,
		vip_address,
		vip_proto,
		ip_address,
		ip_proto,
		port,
		transport_proto
	);
	return 0;
}