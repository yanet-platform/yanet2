#include "array.h"

#include "common/memory.h"
#include "common/memory_address.h"

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
	memory_context_init_from(&array->mctx, mctx, "service_array");
	array->size = 0;
	array->blocks = NULL;
}

void
service_array_free(struct service_array *array) {
	size_t blocks_cnt = service_array_block_count(array);
	struct service_array_block **blocks = ADDR_OF(&array->blocks);
	for (size_t i = 0; i < blocks_cnt; ++i) {
		struct service_array_block *block = ADDR_OF(blocks + i);
		memory_bfree(
			&array->mctx, block, sizeof(struct service_array_block)
		);
	}
	memory_bfree(
		&array->mctx,
		blocks,
		sizeof(struct service_array_block *) * blocks_cnt
	);
}

union service_state *
service_array_lookup(struct service_array *array, size_t idx) {
	if (idx >= array->size) {
		return NULL;
	}
	struct service_array_block **block_rel =
		ADDR_OF(&array->blocks) + idx / SERVICE_REGISTRY_BLOCK_SIZE;
	struct service_array_block *block = ADDR_OF(block_rel);
	return &block->services[idx % SERVICE_REGISTRY_BLOCK_SIZE];
}

int
service_array_push_back(
	struct service_array *array, union service_state *state
) {
	if (array->size % SERVICE_REGISTRY_BLOCK_SIZE == 0) {
		// need allocate new block
		// for this, we reallocate the whole blocks array
		size_t blocks = service_array_block_count(array) + 1;
		struct service_array_block **new_blocks = memory_balloc(
			&array->mctx,
			blocks * sizeof(struct service_array_block *)
		);
		if (new_blocks == NULL) {
			return -1;
		}

		// copy the old blocks
		if (blocks > 1) {
			for (size_t i = 0; i < blocks - 1; ++blocks) {
				EQUATE_OFFSET(
					new_blocks + i,
					ADDR_OF(&array->blocks) + i
				);
			}
		}

		// create and initialize new block
		struct service_array_block *new_block = memory_balloc(
			&array->mctx, sizeof(struct service_array_block)
		);
		if (new_block == NULL) {
			memory_bfree(
				&array->mctx,
				new_blocks,
				sizeof(struct service_array_block *) * blocks
			);
			return -1;
		}

		memset(new_block, 0, sizeof(struct service_array_block));

		SET_OFFSET_OF(&new_blocks[blocks - 1], new_block);

		memory_bfree(
			&array->mctx,
			ADDR_OF(&array->blocks),
			sizeof(struct service_array_block *) * (blocks - 1)
		);

		SET_OFFSET_OF(&array->blocks, new_blocks);
	}

	// initialize service
	array->size++;

	union service_state *service =
		service_array_lookup(array, array->size - 1);
	memcpy(service, state, sizeof(union service_state));
	return 0;
}