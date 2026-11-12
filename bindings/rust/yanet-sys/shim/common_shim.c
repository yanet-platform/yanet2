#include "common_shim.h"

#include "common/fast_lpm_build.h"
#include "common/memory.h"
#include "common/memory_block.h"

int
yanet_sys_common_lpm_init(
	struct fast_lpm *lpm,
	struct memory_context *memory_context,
	const char *name,
	uint8_t key_size
) {
	return fast_lpm_init(lpm, memory_context, name, key_size);
}

int
yanet_sys_common_lpm_insert(
	struct fast_lpm *lpm,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
) {
	return fast_lpm_insert(lpm, from, to, value);
}

int
yanet_sys_common_lpm_build(struct fast_lpm *lpm) {
	return fast_lpm_build(lpm);
}

void
yanet_sys_common_lpm_free(struct fast_lpm *lpm) {
	fast_lpm_free(lpm);
}

void *
yanet_sys_common_memory_balloc(struct memory_context *context, size_t size) {
	return memory_balloc(context, size);
}

void
yanet_sys_common_memory_bfree(
	struct memory_context *context, void *block, size_t size
) {
	memory_bfree(context, block, size);
}

int
yanet_sys_common_memory_context_init_from(
	struct memory_context *context,
	struct memory_context *parent,
	const char *name
) {
	return memory_context_init_from(context, parent, name);
}

void
yanet_sys_common_memory_context_fini(struct memory_context *context) {
	memory_context_fini(context);
}
