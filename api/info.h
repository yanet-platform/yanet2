#pragma once

#include <stdint.h>

#include <sys/types.h>

// FIXME: double declare
#define CP_DEVICE_TYPE_LEN 80
#define CP_DEVICE_NAME_LEN 80

#define CP_MODULE_TYPE_LEN 80
#define CP_MODULE_NAME_LEN 80

#define CP_CHAIN_NAME_LEN 80
#define CP_FUNCTION_NAME_LEN 80
#define CP_PIPELINE_NAME_LEN 80
#define CP_DEVICE_NAME_LEN 80

struct dp_config;

struct dp_module_info {
	char name[80];
};

struct dp_module_list_info {
	uint64_t module_count;
	struct dp_module_info modules[];
};

void
dp_module_list_info_free(struct dp_module_list_info *module_list_info);

struct dp_module_list_info *
yanet_get_dp_module_list_info(struct dp_config *dp_config);

int
yanet_get_dp_module_info(
	struct dp_module_list_info *module_list,
	uint64_t index,
	struct dp_module_info *module_info
);

// Modules

struct cp_module_info {
	char type[CP_MODULE_TYPE_LEN];
	char name[CP_MODULE_NAME_LEN];
	uint64_t gen;
};

struct cp_module_list_info {
	uint64_t module_count;
	struct cp_module_info modules[];
};

void
cp_module_list_info_free(struct cp_module_list_info *module_list_info);

struct cp_module_list_info *
yanet_get_cp_module_list_info(struct dp_config *dp_config);

struct cp_module_info *
yanet_get_cp_module_info(
	struct cp_module_list_info *module_list, uint64_t index
);

// Functions

struct cp_module_info_id {
	char type[CP_MODULE_TYPE_LEN];
	char name[CP_MODULE_NAME_LEN];
};

struct cp_chain_info {
	char name[CP_CHAIN_NAME_LEN];
	uint64_t weight;
	uint64_t length;
	struct cp_module_info_id modules[];
};

struct cp_function_info {
	char name[CP_FUNCTION_NAME_LEN];
	uint64_t chain_count;
	struct cp_chain_info *chains[];
};

struct cp_function_list_info {
	uint64_t function_count;
	struct cp_function_info *functions[];
};

void
cp_function_list_info_free(struct cp_function_list_info *function_list_info);

struct cp_function_list_info *
yanet_get_cp_function_list_info(struct dp_config *dp_config);

struct cp_function_info *
yanet_get_cp_function_info(
	struct cp_function_list_info *function_list, uint64_t index
);

struct cp_chain_info *
yanet_get_cp_function_chain_info(
	struct cp_function_info *function_info, uint64_t index
);

struct cp_module_info_id *
yanet_get_cp_function_chain_module_info(
	struct cp_chain_info *chain_info, uint64_t index
);

// Pipelines

struct cp_function_info_id {
	char name[CP_FUNCTION_NAME_LEN];
};

struct cp_pipeline_info {
	char name[CP_PIPELINE_NAME_LEN];
	uint64_t length;
	struct cp_function_info_id functions[];
};

struct cp_pipeline_list_info {
	uint64_t count;
	struct cp_pipeline_info *pipelines[];
};

void
cp_pipeline_list_info_free(struct cp_pipeline_list_info *pipeline_list_info);

struct cp_pipeline_list_info *
yanet_get_cp_pipeline_list_info(struct dp_config *dp_config);

struct cp_pipeline_info *
yanet_get_cp_pipeline_info(
	struct cp_pipeline_list_info *pipeline_list, uint64_t index
);

struct cp_function_info_id *
yanet_get_cp_pipeline_function_info_id(
	struct cp_pipeline_info *pipeline_info, uint64_t index
);

// Devices

struct cp_device_pipeline_info {
	char name[CP_PIPELINE_NAME_LEN];
	uint64_t weight;
};

struct cp_device_info {
	char type[CP_DEVICE_TYPE_LEN];
	char name[CP_DEVICE_NAME_LEN];
	uint64_t index;
	uint64_t input_count;
	uint64_t output_count;

	struct cp_device_pipeline_info pipelines[];
};

struct cp_device_list_info {
	uint64_t device_count;
	struct cp_device_info *devices[];
};

void
cp_device_list_info_free(struct cp_device_list_info *device_list_info);

struct cp_device_list_info *
yanet_get_cp_device_list_info(struct dp_config *dp_config);

struct cp_device_info *
yanet_get_cp_device_info(
	struct cp_device_list_info *device_list_info, uint64_t idx
);

struct cp_device_pipeline_info *
yanet_get_cp_device_input_pipeline_info(
	struct cp_device_info *device_info, uint64_t idx
);

struct cp_device_pipeline_info *
yanet_get_cp_device_output_pipeline_info(
	struct cp_device_info *device_info, uint64_t idx
);

// Instance

// Length of a memory_context name, matching sizeof(memory_context::name).
#define CP_MCTX_NAME_LEN 64

// One node of an agent's memory_context tree.
//
// Copied to the heap by the info API so the caller never touches shared
// memory.
struct cp_memory_node_info {
	char name[CP_MCTX_NAME_LEN];
	// Index of this node's parent within the enclosing memory_nodes array.
	//
	// UINT32_MAX marks the root node, the agent's own context.
	uint32_t parent_idx;
	uint32_t _pad;
	uint64_t balloc_count;
	uint64_t bfree_count;
	uint64_t balloc_size;
	uint64_t bfree_size;
};

struct cp_agent_instance_info {
	pid_t pid;
	uint64_t memory_limit;
	uint64_t gen;
	// Bytes still available in the agent's allocator.
	//
	// Subtract from memory_limit to see how much memory the agent is
	// currently using.
	uint64_t free_bytes;
	// Flat depth-first snapshot of the agent instance's memory-context
	// subtree.
	//
	// Node 0 is the root, the agent's own context. Every later node's
	// parent_idx points back into this array. Best-effort: a concurrent
	// update may make a node appear or vanish between passes.
	uint64_t memory_node_count;
	struct cp_memory_node_info memory_nodes[];
};

struct cp_agent_info {
	char name[80];
	uint64_t instance_count;
	// Each element is a separately allocated block holding the
	// cp_agent_instance_info header and its trailing memory_nodes array.
	struct cp_agent_instance_info *instances[];
};

struct cp_agent_list_info {
	uint64_t count;
	struct cp_agent_info *agents[];
};

int
yanet_get_cp_agent_instance_info(
	struct cp_agent_info *agent_info,
	uint64_t index,
	struct cp_agent_instance_info **instance_info
);

int
yanet_get_cp_agent_info(
	struct cp_agent_list_info *agent_list_info,
	uint64_t index,
	struct cp_agent_info **agent_info
);

void
cp_agent_list_info_free(struct cp_agent_list_info *agent_list_info);

struct cp_agent_list_info *
yanet_get_cp_agent_list_info(struct dp_config *dp_config);
