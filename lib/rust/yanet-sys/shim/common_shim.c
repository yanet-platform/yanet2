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
