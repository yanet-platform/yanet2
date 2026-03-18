#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/econtext.h"
#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"

#include <assert.h>
#include <stdlib.h>
#include <string.h>

#define DP_MEMORY (1 << 20)
#define CP_MEMORY (1 << 20)

// Allocate a block of the given size, fill it with non-zero garbage,
// and free it back.
//
// The next allocation of the same size will return this recycled block with
// stale content.
static void
pollute_allocator(struct memory_context *ctx, size_t size) {
	void *block = memory_balloc(ctx, size);
	assert(block != NULL);
	memset(block, 0xAB, size);
	memory_bfree(ctx, block, size);
}

int
main(void) {
	void *storage = aligned_alloc(64, DP_MEMORY + CP_MEMORY);
	TEST_ASSERT_NOT_NULL(storage, "failed to allocate storage");
	memset(storage, 0, DP_MEMORY + CP_MEMORY);

	// Set up minimal dp_config + cp_config in shared memory.
	struct dp_config *dp = (struct dp_config *)storage;
	block_allocator_init(&dp->block_allocator);
	block_allocator_put_arena(
		&dp->block_allocator,
		storage + sizeof(struct dp_config),
		DP_MEMORY - sizeof(struct dp_config)
	);
	memory_context_init(&dp->memory_context, "dp", &dp->block_allocator);

	struct cp_config *cp =
		(struct cp_config *)((uintptr_t)storage + DP_MEMORY);
	block_allocator_init(&cp->block_allocator);
	block_allocator_put_arena(
		&cp->block_allocator,
		storage + DP_MEMORY + sizeof(struct cp_config),
		CP_MEMORY - sizeof(struct cp_config)
	);
	memory_context_init(&cp->memory_context, "cp", &cp->block_allocator);
	SET_OFFSET_OF(&dp->cp_config, cp);
	SET_OFFSET_OF(&cp->dp_config, dp);

	// Pollute the allocator pool so the next config_gen allocation
	// returns a recycled block with stale (non-zero) content.
	pollute_allocator(&cp->memory_context, sizeof(struct cp_config_gen));

	// Create config_gen on the recycled block.
	struct agent agent;
	memset(&agent, 0, sizeof(agent));
	memory_context_init_from(
		&agent.memory_context, &cp->memory_context, "test"
	);
	SET_OFFSET_OF(&agent.dp_config, dp);
	SET_OFFSET_OF(&agent.cp_config, cp);

	struct cp_config_gen *gen = cp_config_gen_create(&agent);
	TEST_ASSERT_NOT_NULL(gen, "cp_config_gen_create failed");

	// Reproduce the worker's code path:
	//   config_gen_ectx = ADDR_OF(&cp_config_gen->config_gen_ectx);
	//
	// Without the fix, this resolves the stale offset into a wild
	// pointer. Accessing it triggers ASAN use-after-poison / SIGSEGV.
	// With the fix, ADDR_OF returns NULL and the access is skipped.
	struct config_gen_ectx *ectx = ADDR_OF(&gen->config_gen_ectx);
	if (ectx != NULL) {
		volatile uint64_t x = ectx->device_count;
		(void)x;
	}

	TEST_ASSERT_NULL(
		ectx, "config_gen_ectx must resolve to NULL on fresh gen"
	);

	free(storage);
	return 0;
}
