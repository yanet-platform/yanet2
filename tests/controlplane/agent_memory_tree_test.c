#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "api/info.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"
#include "common/strutils.h"
#include "common/test_assert.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/agent.h"
#include "lib/dataplane/config/bootstrap.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#define DP_MEMORY (4 << 20)
#define CP_MEMORY (4 << 20)
#define STORAGE_SIZE (DP_MEMORY + CP_MEMORY)

// Verifies that memory_context_count_nodes and memory_context_fill_nodes
// export a correct depth-first tree via yanet_get_cp_agent_list_info. Models
// an agent_attach'd agent: the memory_context tree is rooted at the agent's
// own block_allocator, the same allocator build_instance_info queries via
// block_allocator_free_size, so a future regression that moves that call
// inside the tree-walk lock would deadlock this test instead of passing.
static int
test_memory_tree_exported(void) {
	void *storage = calloc(1, STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp = NULL;
	struct cp_config *cp = NULL;
	int rc = dp_storage_init(0, 0, storage, DP_MEMORY, CP_MEMORY, &dp, &cp);
	TEST_ASSERT(rc == 0, "dp_storage_init failed");

	cp_config_lock(cp);

	struct agent *sys = dp_system_agent_new(cp, dp, "dataplane");
	TEST_ASSERT_NOT_NULL(sys, "dp_system_agent_new failed");

	yanet_error *err = NULL;
	struct cp_config_gen *gen = cp_config_gen_new(sys, &err);
	TEST_ASSERT_NOT_NULL(gen, "cp_config_gen_new failed");
	SET_OFFSET_OF(&cp->cp_config_gen, gen);

	// Build a small named agent directly in shared memory.
	struct agent *ag =
		(struct agent *)memory_balloc(&cp->memory_context, sizeof(*ag));
	TEST_ASSERT_NOT_NULL(ag, "agent alloc failed");
	memset(ag, 0, sizeof(*ag));
	strtcpy(ag->name, "myagent", sizeof(ag->name));
	ag->memory_limit = 65536;
	ag->pid = 42;
	ag->gen = 1;
	SET_OFFSET_OF(&ag->dp_config, dp);
	SET_OFFSET_OF(&ag->cp_config, cp);
	block_allocator_init(&ag->block_allocator);
	memory_context_init(
		&ag->memory_context, "myagent", &ag->block_allocator
	);

	// Seed the agent's own allocator with a real arena, as agent_attach
	// does, so the child contexts below actually allocate from it.
	void *arena = memory_balloc(&cp->memory_context, ag->memory_limit);
	TEST_ASSERT_NOT_NULL(arena, "arena alloc failed");
	block_allocator_put_arena(
		&ag->block_allocator, arena, ag->memory_limit
	);

	// Add a child context "mod" and a grandchild "lpm".
	struct memory_context *mod_ctx = (struct memory_context *)memory_balloc(
		&cp->memory_context, sizeof(*mod_ctx)
	);
	TEST_ASSERT_NOT_NULL(mod_ctx, "mod_ctx alloc failed");
	memory_context_init_from(mod_ctx, &ag->memory_context, "mod");

	struct memory_context *lpm_ctx = (struct memory_context *)memory_balloc(
		&cp->memory_context, sizeof(*lpm_ctx)
	);
	TEST_ASSERT_NOT_NULL(lpm_ctx, "lpm_ctx alloc failed");
	memory_context_init_from(lpm_ctx, mod_ctx, "lpm");

	// Do a couple of allocations so balloc_size is non-zero.
	void *b1 = memory_balloc(mod_ctx, 128);
	TEST_ASSERT_NOT_NULL(b1, "mod alloc failed");
	void *b2 = memory_balloc(lpm_ctx, 256);
	TEST_ASSERT_NOT_NULL(b2, "lpm alloc failed");

	// Register the agent in the registry.
	struct cp_agent_registry *old_reg = ADDR_OF(&cp->agent_registry);
	struct cp_agent_registry *new_reg =
		(struct cp_agent_registry *)memory_balloc(
			&cp->memory_context,
			sizeof(struct cp_agent_registry) +
				(old_reg->count + 1) * sizeof(struct agent *)
		);
	TEST_ASSERT_NOT_NULL(new_reg, "registry alloc failed");
	for (uint64_t idx = 0; idx < old_reg->count; ++idx) {
		SET_OFFSET_OF(
			&new_reg->agents[idx], ADDR_OF(&old_reg->agents[idx])
		);
	}
	SET_OFFSET_OF(&new_reg->agents[old_reg->count], ag);
	new_reg->count = old_reg->count + 1;
	SET_OFFSET_OF(&cp->agent_registry, new_reg);

	dp->instance_count = 1;
	cp_config_unlock(cp);
	dp_config_mark_ready(dp);

	struct cp_agent_list_info *list = yanet_get_cp_agent_list_info(dp);
	TEST_ASSERT_NOT_NULL(
		list, "yanet_get_cp_agent_list_info returned NULL"
	);

	// Find our agent in the list.
	struct cp_agent_info *ai = NULL;
	for (uint64_t idx = 0; idx < list->count; ++idx) {
		struct cp_agent_info *candidate = list->agents[idx];
		if (strncmp(candidate->name, "myagent", 80) == 0) {
			ai = candidate;
			break;
		}
	}
	TEST_ASSERT_NOT_NULL(ai, "myagent not found in agent list");
	TEST_ASSERT(ai->instance_count >= 1, "instance_count must be >= 1");

	struct cp_agent_instance_info *inst = NULL;
	rc = yanet_get_cp_agent_instance_info(ai, 0, &inst);
	TEST_ASSERT(rc == 0, "yanet_get_cp_agent_instance_info failed");
	TEST_ASSERT_NOT_NULL(inst, "instance info is NULL");

	// The tree has root "myagent", child "mod", grandchild "lpm" — 3 nodes.
	TEST_ASSERT(inst->memory_node_count == 3, "expected 3 memory nodes");

	// Node 0 must be the root with UINT32_MAX parent and name "myagent".
	TEST_ASSERT(
		inst->memory_nodes[0].parent_idx == UINT32_MAX,
		"root node must have parent_idx == UINT32_MAX"
	);
	TEST_ASSERT(
		strncmp(inst->memory_nodes[0].name, "myagent", CP_MCTX_NAME_LEN
		) == 0,
		"root node name must be myagent"
	);

	// The child "mod" has parent_idx 0 (the root) and non-zero balloc_size.
	int found_mod = 0;
	int found_lpm = 0;
	uint64_t mod_idx = UINT64_MAX;
	for (uint64_t n = 1; n < inst->memory_node_count; ++n) {
		struct cp_memory_node_info *node = &inst->memory_nodes[n];
		if (strncmp(node->name, "mod", CP_MCTX_NAME_LEN) == 0) {
			found_mod = 1;
			mod_idx = n;
			TEST_ASSERT(
				node->parent_idx == 0,
				"mod parent_idx must be 0"
			);
			TEST_ASSERT(
				node->balloc_size > 0,
				"mod must have non-zero balloc_size"
			);
		} else if (strncmp(node->name, "lpm", CP_MCTX_NAME_LEN) == 0) {
			found_lpm = 1;
			TEST_ASSERT(
				node->balloc_size > 0,
				"lpm must have non-zero balloc_size"
			);
			TEST_ASSERT(
				node->parent_idx == mod_idx,
				"lpm parent_idx must be mod's node index"
			);
		}
	}
	TEST_ASSERT(found_mod, "mod node not found");
	TEST_ASSERT(found_lpm, "lpm node not found");

	cp_agent_list_info_free(list);
	free(storage);
	return TEST_SUCCESS;
}

// Verifies that cp_agent_list_info_free(NULL) does not crash.
static int
test_free_null_safe(void) {
	cp_agent_list_info_free(NULL);
	return TEST_SUCCESS;
}

// Verifies that after memory_context_fini on the grandchild a fresh snapshot
// no longer reports that node and the root name is still intact. Models a
// dp_system_agent_new agent: the memory_context tree is rooted at cp_config's
// allocator, distinct from the agent's own block_allocator, covering the
// other of the two allocator shapes build_instance_info handles.
static int
test_memory_tree_after_fini(void) {
	void *storage = calloc(1, STORAGE_SIZE);
	TEST_ASSERT_NOT_NULL(storage, "calloc failed");

	struct dp_config *dp = NULL;
	struct cp_config *cp = NULL;
	int rc = dp_storage_init(0, 0, storage, DP_MEMORY, CP_MEMORY, &dp, &cp);
	TEST_ASSERT(rc == 0, "dp_storage_init failed");

	cp_config_lock(cp);

	struct agent *sys = dp_system_agent_new(cp, dp, "dataplane");
	TEST_ASSERT_NOT_NULL(sys, "dp_system_agent_new failed");

	yanet_error *err = NULL;
	struct cp_config_gen *gen = cp_config_gen_new(sys, &err);
	TEST_ASSERT_NOT_NULL(gen, "cp_config_gen_new failed");
	SET_OFFSET_OF(&cp->cp_config_gen, gen);

	struct agent *ag =
		(struct agent *)memory_balloc(&cp->memory_context, sizeof(*ag));
	TEST_ASSERT_NOT_NULL(ag, "agent alloc failed");
	memset(ag, 0, sizeof(*ag));
	strtcpy(ag->name, "fini-agent", sizeof(ag->name));
	ag->memory_limit = 65536;
	ag->pid = 7;
	ag->gen = 1;
	SET_OFFSET_OF(&ag->dp_config, dp);
	SET_OFFSET_OF(&ag->cp_config, cp);
	block_allocator_init(&ag->block_allocator);
	memory_context_init(
		&ag->memory_context, "fini-agent", &cp->block_allocator
	);

	struct memory_context *child_ctx = (struct memory_context *)
		memory_balloc(&cp->memory_context, sizeof(*child_ctx));
	TEST_ASSERT_NOT_NULL(child_ctx, "child_ctx alloc failed");
	memory_context_init_from(child_ctx, &ag->memory_context, "child");

	struct memory_context *grand_ctx = (struct memory_context *)
		memory_balloc(&cp->memory_context, sizeof(*grand_ctx));
	TEST_ASSERT_NOT_NULL(grand_ctx, "grand_ctx alloc failed");
	memory_context_init_from(grand_ctx, child_ctx, "grand");

	struct cp_agent_registry *old_reg = ADDR_OF(&cp->agent_registry);
	struct cp_agent_registry *new_reg =
		(struct cp_agent_registry *)memory_balloc(
			&cp->memory_context,
			sizeof(struct cp_agent_registry) +
				(old_reg->count + 1) * sizeof(struct agent *)
		);
	TEST_ASSERT_NOT_NULL(new_reg, "registry alloc failed");
	for (uint64_t idx = 0; idx < old_reg->count; ++idx) {
		SET_OFFSET_OF(
			&new_reg->agents[idx], ADDR_OF(&old_reg->agents[idx])
		);
	}
	SET_OFFSET_OF(&new_reg->agents[old_reg->count], ag);
	new_reg->count = old_reg->count + 1;
	SET_OFFSET_OF(&cp->agent_registry, new_reg);

	dp->instance_count = 1;
	cp_config_unlock(cp);
	dp_config_mark_ready(dp);

	// Tear down the grandchild context, then re-snapshot.
	memory_context_fini(grand_ctx);

	struct cp_agent_list_info *list = yanet_get_cp_agent_list_info(dp);
	TEST_ASSERT_NOT_NULL(
		list, "yanet_get_cp_agent_list_info returned NULL"
	);

	struct cp_agent_info *ai = NULL;
	for (uint64_t idx = 0; idx < list->count; ++idx) {
		struct cp_agent_info *candidate = list->agents[idx];
		if (strncmp(candidate->name, "fini-agent", 80) == 0) {
			ai = candidate;
			break;
		}
	}
	TEST_ASSERT_NOT_NULL(ai, "fini-agent not found in agent list");

	struct cp_agent_instance_info *inst = NULL;
	rc = yanet_get_cp_agent_instance_info(ai, 0, &inst);
	TEST_ASSERT(rc == 0, "yanet_get_cp_agent_instance_info failed");
	TEST_ASSERT_NOT_NULL(inst, "instance info is NULL");

	// After fini of grand, only root + child remain — 2 nodes.
	TEST_ASSERT(
		inst->memory_node_count == 2,
		"expected 2 memory nodes after fini"
	);

	// Root name must still be intact.
	TEST_ASSERT(
		strncmp(inst->memory_nodes[0].name,
			"fini-agent",
			CP_MCTX_NAME_LEN) == 0,
		"root node name must still be fini-agent after grandchild fini"
	);

	// "grand" must not appear.
	for (uint64_t n = 0; n < inst->memory_node_count; ++n) {
		TEST_ASSERT(
			strncmp(inst->memory_nodes[n].name,
				"grand",
				CP_MCTX_NAME_LEN) != 0,
			"grand must not appear after memory_context_fini"
		);
	}

	cp_agent_list_info_free(list);
	free(storage);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	size_t tests_count = 0;
	size_t tests_failed = 0;

	++tests_count;
	if (test_memory_tree_exported() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR, "test_memory_tree_exported failed");
	}

	++tests_count;
	if (test_free_null_safe() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR, "test_free_null_safe failed");
	}

	++tests_count;
	if (test_memory_tree_after_fini() != TEST_SUCCESS) {
		++tests_failed;
		LOG(ERROR, "test_memory_tree_after_fini failed");
	}

	if (tests_failed != 0) {
		LOG(ERROR, "%zu/%zu tests failed", tests_failed, tests_count);
		return -1;
	}

	LOG(INFO, "all %zu tests passed", tests_count);
	return 0;
}
