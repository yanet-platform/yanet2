#include "common_shim.h"

#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"

int
yanet_sys_common_lpm_init(
	struct lpm *lpm, struct memory_context *memory_context, const char *name
) {
	return lpm_init(lpm, memory_context, name);
}

int
yanet_sys_common_lpm_insert(
	struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
) {
	return lpm_insert(lpm, key_size, from, to, value);
}

void
yanet_sys_common_lpm_free(struct lpm *lpm) {
	lpm_free(lpm);
}

int
yanet_sys_common_block_allocator_init(struct block_allocator *allocator) {
	return block_allocator_init(allocator);
}

void
yanet_sys_common_block_allocator_put_arena(
	struct block_allocator *allocator, void *arena, size_t size
) {
	block_allocator_put_arena(allocator, arena, size);
}

size_t
yanet_sys_common_block_allocator_free_size(struct block_allocator *allocator) {
	return block_allocator_free_size(allocator);
}

int
yanet_sys_common_memory_context_init(
	struct memory_context *context,
	const char *name,
	struct block_allocator *allocator
) {
	return memory_context_init(context, name, allocator);
}
