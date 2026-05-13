#include "api/agent.h"
#include "api/info.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"

#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define DP_MEMORY (1u << 22)
#define CP_MEMORY (1u << 22)

// Build a minimal dp_config + cp_config pair in a pre-allocated block so we
// can call agent_attach and yanet_get_cp_agent_list_info without a real
// shared-memory file.
static int
setup_dp_cp(
	void *storage, struct dp_config **dp_out, struct cp_config **cp_out
) {
	struct dp_config *dp = (struct dp_config *)storage;
	block_allocator_init(&dp->block_allocator);
	block_allocator_put_arena(
		&dp->block_allocator,
		(char *)storage + sizeof(struct dp_config),
		DP_MEMORY - sizeof(struct dp_config)
	);
	memory_context_init(&dp->memory_context, "dp", &dp->block_allocator);

	struct cp_config *cp =
		(struct cp_config *)((char *)storage + DP_MEMORY);
	block_allocator_init(&cp->block_allocator);
	block_allocator_put_arena(
		&cp->block_allocator,
		(char *)storage + DP_MEMORY + sizeof(struct cp_config),
		CP_MEMORY - sizeof(struct cp_config)
	);
	memory_context_init(&cp->memory_context, "cp", &cp->block_allocator);

	SET_OFFSET_OF(&dp->cp_config, cp);
	SET_OFFSET_OF(&cp->dp_config, dp);

	// Initialise the agent registry to an empty one.
	struct cp_agent_registry *reg =
		(struct cp_agent_registry *)memory_balloc(
			&cp->memory_context, sizeof(struct cp_agent_registry)
		);
	TEST_ASSERT_NOT_NULL(reg, "failed to alloc agent_registry");
	memset(reg, 0, sizeof(struct cp_agent_registry));
	SET_OFFSET_OF(&cp->agent_registry, reg);

	struct agent bootstrap;
	memset(&bootstrap, 0, sizeof(bootstrap));
	memory_context_init_from(
		&bootstrap.memory_context, &cp->memory_context, "bootstrap"
	);
	SET_OFFSET_OF(&bootstrap.dp_config, dp);
	SET_OFFSET_OF(&bootstrap.cp_config, cp);

	yanet_error *err = NULL;
	struct cp_config_gen *config_gen =
		cp_config_gen_create(&bootstrap, &err);
	TEST_ASSERT_NOT_NULL(config_gen, "cp_config_gen_create failed");
	SET_OFFSET_OF(&cp->cp_config_gen, config_gen);

	*dp_out = dp;
	*cp_out = cp;
	return 0;
}

// NOTE: after attaching one agent and adding two child memory_contexts to
// simulate two cp_modules (one of which has a deeper filter->lpm chain),
// yanet_get_cp_agent_list_info should return a flat DFS-ordered node list
// with the correct parent_idx values and the right total count.
static int
test_memory_tree_exported(void) {
	void *storage = aligned_alloc(64, DP_MEMORY + CP_MEMORY);
	TEST_ASSERT_NOT_NULL(storage, "failed to allocate storage");
	memset(storage, 0, DP_MEMORY + CP_MEMORY);

	struct dp_config *dp;
	struct cp_config *cp;
	int rc = setup_dp_cp(storage, &dp, &cp);
	if (rc != 0) {
		free(storage);
		return rc;
	}

	// Attach an agent so it appears in the registry.
	yanet_error *err = NULL;
	struct agent *ag = agent_attach(
		(struct yanet_shm *)dp, 0, "test_agent", 1u << 20, &err
	);
	TEST_ASSERT_NOT_NULL(ag, "agent_attach failed");

	// Simulate two cp_modules hanging off the agent's memory_context.
	// mod_fwd gets a deeper chain: filter -> lpm.
	struct memory_context mod_fwd, mod_dec, filter, lpm;
	memory_context_init_from(&mod_fwd, &ag->memory_context, "mod_fwd");
	memory_context_init_from(&mod_dec, &ag->memory_context, "mod_dec");
	memory_context_init_from(&filter, &mod_fwd, "filter");
	memory_context_init_from(&lpm, &filter, "lpm");

	// Expected DFS order.
	//
	// Note that head-insertion reverses siblings, so mod_dec is first_child
	// of agent, then mod_fwd:
	//   0: test_agent  (root, parent_idx = UINT32_MAX)
	//   1: mod_dec     (parent 0)
	//   2: mod_fwd     (parent 0)
	//   3: filter      (parent 2)
	//   4: lpm         (parent 3)
	const uint64_t expected_nodes = 5;

	struct cp_agent_list_info *list = yanet_get_cp_agent_list_info(dp);
	TEST_ASSERT_NOT_NULL(
		list, "yanet_get_cp_agent_list_info returned NULL"
	);
	TEST_ASSERT(list->count == 1, "expected exactly 1 agent");

	struct cp_agent_info *ai;
	rc = yanet_get_cp_agent_info(list, 0, &ai);
	TEST_ASSERT(rc == 0, "yanet_get_cp_agent_info failed");
	TEST_ASSERT(
		strncmp(ai->name, "test_agent", sizeof(ai->name)) == 0,
		"agent name mismatch"
	);
	TEST_ASSERT(ai->instance_count == 1, "expected 1 instance");

	struct cp_agent_instance_info *inst;
	rc = yanet_get_cp_agent_instance_info(ai, 0, &inst);
	TEST_ASSERT(rc == 0, "yanet_get_cp_agent_instance_info failed");
	TEST_ASSERT(
		inst->memory_node_count == expected_nodes,
		"expected %lu memory nodes, got %lu",
		(unsigned long)expected_nodes,
		(unsigned long)inst->memory_node_count
	);

	// Node 0 is the root.
	TEST_ASSERT(
		strncmp(inst->memory_nodes[0].name,
			"test_agent",
			CP_MCTX_NAME_LEN) == 0,
		"node[0] name should be test_agent"
	);
	TEST_ASSERT(
		inst->memory_nodes[0].parent_idx == UINT32_MAX,
		"root node parent_idx must be UINT32_MAX"
	);

	// Every non-root node must have parent_idx strictly less than its
	// own index.
	//
	// Because of the DFS invariant: parent always comes before child.
	for (uint64_t idx = 1; idx < inst->memory_node_count; ++idx) {
		TEST_ASSERT(
			inst->memory_nodes[idx].parent_idx < (uint32_t)idx,
			"node[%lu] parent_idx %u >= idx - not DFS order",
			(unsigned long)idx,
			inst->memory_nodes[idx].parent_idx
		);
	}

	// Verify that named nodes are present and parents are correct.
	//
	// Because children are inserted at the head of the chain, mod_dec
	// (added after mod_fwd) appears first in DFS order.
	TEST_ASSERT(
		strncmp(inst->memory_nodes[1].name, "mod_dec", CP_MCTX_NAME_LEN
		) == 0,
		"node[1] should be mod_dec"
	);
	TEST_ASSERT(
		inst->memory_nodes[1].parent_idx == 0,
		"mod_dec parent should be 0 (test_agent)"
	);

	TEST_ASSERT(
		strncmp(inst->memory_nodes[2].name, "mod_fwd", CP_MCTX_NAME_LEN
		) == 0,
		"node[2] should be mod_fwd"
	);
	TEST_ASSERT(
		inst->memory_nodes[2].parent_idx == 0,
		"mod_fwd parent should be 0 (test_agent)"
	);

	TEST_ASSERT(
		strncmp(inst->memory_nodes[3].name, "filter", CP_MCTX_NAME_LEN
		) == 0,
		"node[3] should be filter"
	);
	TEST_ASSERT(
		inst->memory_nodes[3].parent_idx == 2,
		"filter parent should be 2 (mod_fwd)"
	);

	TEST_ASSERT(
		strncmp(inst->memory_nodes[4].name, "lpm", CP_MCTX_NAME_LEN) ==
			0,
		"node[4] should be lpm"
	);
	TEST_ASSERT(
		inst->memory_nodes[4].parent_idx == 3,
		"lpm parent should be 3 (filter)"
	);

	cp_agent_list_info_free(list);
	free(storage);
	return 0;
}

static int
test_free_null_safe(void) {
	cp_agent_list_info_free(NULL);
	return 0;
}

// NOTE: after tearing down child contexts, a fresh info call reflects the
// reduced node count - only the root remains.
static int
test_memory_tree_after_fini(void) {
	void *storage = aligned_alloc(64, DP_MEMORY + CP_MEMORY);
	TEST_ASSERT_NOT_NULL(storage, "failed to allocate storage");
	memset(storage, 0, DP_MEMORY + CP_MEMORY);

	struct dp_config *dp;
	struct cp_config *cp;
	int rc = setup_dp_cp(storage, &dp, &cp);
	if (rc != 0) {
		free(storage);
		return rc;
	}

	yanet_error *err = NULL;
	struct agent *ag = agent_attach(
		(struct yanet_shm *)dp, 0, "solo_agent", 1u << 20, &err
	);
	TEST_ASSERT_NOT_NULL(ag, "agent_attach failed");

	// Attach a child, then immediately detach it.
	struct memory_context child;
	memory_context_init_from(&child, &ag->memory_context, "transient");
	memory_context_fini(&child);

	struct cp_agent_list_info *list = yanet_get_cp_agent_list_info(dp);
	TEST_ASSERT_NOT_NULL(
		list, "yanet_get_cp_agent_list_info returned NULL"
	);

	struct cp_agent_info *ai;
	yanet_get_cp_agent_info(list, 0, &ai);

	struct cp_agent_instance_info *inst;
	yanet_get_cp_agent_instance_info(ai, 0, &inst);

	// After fini the child is gone; only the root context remains.
	TEST_ASSERT(
		inst->memory_node_count == 1,
		"expected only root node after child fini, got %lu",
		(unsigned long)inst->memory_node_count
	);
	TEST_ASSERT(
		strncmp(inst->memory_nodes[0].name,
			"solo_agent",
			CP_MCTX_NAME_LEN) == 0,
		"root node name mismatch after child fini"
	);

	cp_agent_list_info_free(list);
	free(storage);
	return 0;
}

int
main(void) {
	log_enable_name("info");

	if (test_memory_tree_exported() != 0) {
		LOG(ERROR, "test_memory_tree_exported failed");
		return -1;
	}
	if (test_free_null_safe() != 0) {
		LOG(ERROR, "test_free_null_safe failed");
		return -1;
	}
	if (test_memory_tree_after_fini() != 0) {
		LOG(ERROR, "test_memory_tree_after_fini failed");
		return -1;
	}

	LOG(INFO, "agent_memory_tree tests: OK");
	return 0;
}
