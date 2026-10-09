#include "cp_shim.h"

#include <string.h>

#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"

void *
yanet_sys_cp_balloc(struct agent *agent, size_t size) {
	void *block = memory_balloc(&agent->memory_context, size);
	if (block != NULL) {
		memset(block, 0, size);
	}
	return block;
}

void
yanet_sys_cp_bfree(struct agent *agent, void *block, size_t size) {
	memory_bfree(&agent->memory_context, block, size);
}

int
yanet_sys_cp_lpm_init(
	struct lpm *lpm, struct cp_module *owner, const char *name
) {
	return lpm_init(lpm, &owner->memory_context, name);
}

void
yanet_sys_cp_lpm_free(struct lpm *lpm) {
	lpm_free(lpm);
}

int
yanet_sys_cp_lpm_insert(
	struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
) {
	return lpm_insert(lpm, key_size, from, to, value);
}

struct agent *
yanet_sys_cp_module_agent(struct cp_module *cp_module) {
	return ADDR_OF(&cp_module->agent);
}

size_t
yanet_sys_cp_agent_arenas(
	struct agent *agent, uintptr_t *starts, uint64_t *sizes, size_t capacity
) {
	struct agent_arena *arenas = ADDR_OF(&agent->arenas);
	for (size_t idx = 0; idx < agent->arena_count && idx < capacity;
	     ++idx) {
		starts[idx] = (uintptr_t)ADDR_OF(&arenas[idx].data);
		sizes[idx] = arenas[idx].size;
	}
	return agent->arena_count;
}
