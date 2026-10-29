#include "common_shim.h"

#include "common/lpm.h"
#include "common/memory.h"

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
