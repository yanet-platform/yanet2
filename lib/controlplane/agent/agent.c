#include "agent.h"

#include <linux/mman.h>
#include <sys/mman.h>
#include <sys/stat.h>

#include <fcntl.h>
#include <unistd.h>

#include <errno.h>

#include "common/memory.h"
#include "common/strutils.h"

#include "controlplane/config/zone.h"
#include "controlplane/diag/diag.h"
#include "dataplane/config/zone.h"

#include "api/agent.h"
#include "diag.h"

#include <stdio.h>

#define AGENT_TRY(agent, call, ...)                                            \
	DIAG_TRY(&(agent->diag), call, ##__VA_ARGS__);

struct yanet_shm *
yanet_shm_attach(const char *path) {
	int fd = open(path, O_RDWR, S_IRUSR | S_IWUSR);
	if (fd == -1) {
		return NULL;
	}

	struct stat stat;
	int rc = fstat(fd, &stat);
	if (rc == -1) {
		close(fd);
		return NULL;
	}

	void *ptr = mmap(
		NULL, stat.st_size, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0
	);
	close(fd);
	if (ptr == MAP_FAILED) {
		return NULL;
	}

	struct yanet_shm *shm = (struct yanet_shm *)ptr;
	return shm;
}

int
yanet_shm_detach(struct yanet_shm *shm) {
	// calculate total size of shared memory
	size_t size = 0;
	struct dp_config *dp_config = (struct dp_config *)shm;
	uint32_t instance_count = dp_config->instance_count;
	for (uint32_t instance_idx = 0; instance_idx < instance_count;
	     ++instance_idx) {
		size += dp_config->storage_size;
		dp_config = dp_config_nextk(dp_config, 1);
	}

	return munmap(shm, size);
}

struct dp_config *
yanet_shm_dp_config(struct yanet_shm *shm, uint32_t instance_idx) {
	return dp_config_nextk((struct dp_config *)shm, instance_idx);
}

uint32_t
yanet_shm_instance_count(struct yanet_shm *shm) {
	struct dp_config *dp_config = yanet_shm_dp_config(shm, 0);
	return dp_config->instance_count;
}

uint32_t
dataplane_instance_numa_idx(struct dp_config *dp_config) {
	return dp_config->numa_idx;
}

uint32_t
dataplane_instance_worker_count(struct dp_config *dp_config) {
	return (uint32_t)dp_config->worker_count;
}

struct agent *
agent_attach(
	struct yanet_shm *shm,
	uint32_t instance_idx,
	const char *agent_name,
	size_t memory_limit
) {
	struct dp_config *dp_config = yanet_shm_dp_config(shm, instance_idx);

	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);

	cp_config_lock(cp_config);

	struct agent *new_agent = (struct agent *)memory_balloc(
		&cp_config->memory_context, sizeof(struct agent)
	);
	if (new_agent == NULL) {
		goto unlock;
	}
	memset(new_agent, 0, sizeof(struct agent));

	strtcpy(new_agent->name, agent_name, sizeof(new_agent->name));
	new_agent->memory_limit = memory_limit;
	SET_OFFSET_OF(&new_agent->dp_config, dp_config);
	SET_OFFSET_OF(&new_agent->cp_config, cp_config);
	new_agent->pid = getpid();

	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);
	new_agent->gen = config_gen->gen;

	block_allocator_init(&new_agent->block_allocator);
	memory_context_init(
		&new_agent->memory_context,
		agent_name,
		&new_agent->block_allocator
	);

	/*
	 * FIXME: the code bellow tries to allocate memory_limit bytes
	 * using max possible chunk size what breaks allocator encapsulation.
	 * Alternative multi-alloc api should be implemented.
	 */
	uint64_t arena_count =
		(memory_limit + MEMORY_BLOCK_ALLOCATOR_MAX_SIZE - 1) /
		MEMORY_BLOCK_ALLOCATOR_MAX_SIZE;
	struct agent_arena *arenas = (struct agent_arena *)memory_balloc(
		&cp_config->memory_context,
		sizeof(struct agent_arena) * arena_count
	);
	if (arenas == NULL) {
		agent_cleanup(new_agent);
		new_agent = NULL;
		goto unlock;
	}

	memset(arenas, 0, sizeof(struct agent_arena) * arena_count);
	SET_OFFSET_OF(&new_agent->arenas, arenas);

	while (new_agent->arena_count < arena_count) {
		uint64_t arena_size =
			memory_limit > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE
				? MEMORY_BLOCK_ALLOCATOR_MAX_SIZE
				: memory_limit;

		void *arena =
			memory_balloc(&cp_config->memory_context, arena_size);
		if (arena == NULL) {
			agent_cleanup(new_agent);
			new_agent = NULL;
			goto unlock;
		}
		block_allocator_put_arena(
			&new_agent->block_allocator, arena, arena_size
		);
		SET_OFFSET_OF(&arenas[new_agent->arena_count].data, arena);
		arenas[new_agent->arena_count].size = arena_size;
		new_agent->arena_count++;

		memory_limit -= arena_size;
	}

	struct cp_agent_registry *old_registry =
		ADDR_OF(&cp_config->agent_registry);
	bool found = false;
	for (uint64_t agent_idx = 0; agent_idx < old_registry->count;
	     ++agent_idx) {
		struct agent *old_agent =
			ADDR_OF(&old_registry->agents[agent_idx]);
		if (!strncmp(old_agent->name, agent_name, 80)) {
			found = true;
			SET_OFFSET_OF(
				&old_registry->agents[agent_idx], new_agent
			);
			SET_OFFSET_OF(&new_agent->prev, old_agent);
			break;
		}
	}

	if (!found) {
		new_agent->prev = NULL;
		struct cp_agent_registry *new_registry =
			(struct cp_agent_registry *)memory_balloc(
				&cp_config->memory_context,
				sizeof(struct cp_agent_registry) +
					(old_registry->count + 1) *
						sizeof(struct agent *)
			);
		if (new_registry == NULL) {
			agent_cleanup(new_agent);
			new_agent = NULL;
			goto unlock;
		}

		new_registry->count = old_registry->count + 1;
		for (uint64_t agent_idx = 0; agent_idx < old_registry->count;
		     ++agent_idx) {
			SET_OFFSET_OF(
				&new_registry->agents[agent_idx],
				ADDR_OF(&old_registry->agents[agent_idx])
			);
		}
		SET_OFFSET_OF(
			&new_registry->agents[new_registry->count - 1],
			new_agent
		);

		memory_bfree(
			&cp_config->memory_context,
			old_registry,
			sizeof(struct cp_agent_registry) +
				sizeof(struct agent *) * old_registry->count
		);

		SET_OFFSET_OF(&cp_config->agent_registry, new_registry);
	}

	struct agent *agent = new_agent;
	while (ADDR_OF(&agent->prev) != NULL) {
		struct agent *prev_agent = ADDR_OF(&agent->prev);

		if (prev_agent->loaded_module_count == 0) {
			SET_OFFSET_OF(&agent->prev, ADDR_OF(&prev_agent->prev));
			agent_cleanup(prev_agent);
			continue;
		}

		agent = ADDR_OF(&agent->prev);
	}

unlock:
	cp_config_unlock(cp_config);

	return new_agent;
}

void
agent_cleanup(struct agent *agent) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	struct agent_arena *arenas = ADDR_OF(&agent->arenas);
	if (arenas) {
		for (uint64_t arena_idx = 0; arena_idx < agent->arena_count;
		     ++arena_idx) {
			memory_bfree(
				&cp_config->memory_context,
				ADDR_OF(&arenas[arena_idx].data),
				arenas[arena_idx].size
			);
		}
		memory_bfree(
			&cp_config->memory_context,
			arenas,
			sizeof(struct agent_arena) * agent->arena_count
		);
	}
	memory_bfree(&cp_config->memory_context, agent, sizeof(struct agent));
}

int
agent_detach(struct agent *agent) {
	(void)agent;
	// NOTE: Currently a no-op.
	return 0;
}

int
agent_update_modules(
	struct agent *agent, size_t module_count, struct cp_module **modules
) {
	int res = AGENT_TRY(
		agent,
		cp_config_update_modules(
			ADDR_OF(&agent->dp_config),
			ADDR_OF(&agent->cp_config),
			module_count,
			modules
		),
		"failed to update modules"
	);

	agent_free_unused_agents(agent);

	return res;
}

int
agent_delete_module(
	struct agent *agent, const char *module_type, const char *module_name
) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	int res = AGENT_TRY(
		agent,
		cp_config_delete_module(
			dp_config, cp_config, module_type, module_name
		),
		"failed to delete module"
	);

	agent_free_unused_agents(agent);

	return res;
}

struct cp_chain_config *
cp_chain_config_create(
	const char *name,
	uint64_t length,
	const char *const *types,
	const char *const *names
) {
	struct cp_chain_config *cp_chain_config = (struct cp_chain_config *)
		calloc(1,
		       sizeof(struct cp_chain_config) +
			       sizeof(struct cp_chain_module_config) * length);
	if (cp_chain_config == NULL)
		return NULL;

	strtcpy(cp_chain_config->name, name, CP_CHAIN_NAME_LEN);
	cp_chain_config->length = length;

	for (uint64_t idx = 0; idx < length; ++idx) {
		strtcpy(cp_chain_config->modules[idx].type,
			types[idx],
			sizeof(cp_chain_config->modules[idx].type));
		strtcpy(cp_chain_config->modules[idx].name,
			names[idx],
			sizeof(cp_chain_config->modules[idx].name));
	}
	return cp_chain_config;
}

void
cp_chain_config_free(struct cp_chain_config *cp_chain_config) {
	free(cp_chain_config);
}

struct cp_function_config *
cp_function_config_create(const char *name, uint64_t chain_count) {
	struct cp_function_config *config = (struct cp_function_config *)calloc(
		1,
		sizeof(struct cp_function_config) +
			sizeof(struct cp_function_chain_config) * chain_count
	);
	if (config == NULL)
		return NULL;
	strtcpy(config->name, name, CP_FUNCTION_NAME_LEN);
	config->chain_count = chain_count;

	return config;
}

void
cp_function_config_free(struct cp_function_config *config) {
	for (uint64_t idx = 0; idx < config->chain_count; ++idx) {
		if (config->chains[idx].chain != NULL)
			cp_chain_config_free(config->chains[idx].chain);
	}
	free(config);
}

int
cp_function_config_set_chain(
	struct cp_function_config *cp_function_config,
	uint64_t index,
	struct cp_chain_config *cp_chain_config,
	uint64_t weight
) {
	if (index >= cp_function_config->chain_count)
		return -1;

	if (cp_function_config->chains[index].chain != NULL)
		return -1;

	cp_function_config->chains[index] = (struct cp_function_chain_config){
		.chain = cp_chain_config,
		.weight = weight,
	};

	return 0;
}

int
agent_update_functions(
	struct agent *agent,
	uint64_t function_count,
	struct cp_function_config *functions[]
) {
	return AGENT_TRY(
		agent,
		cp_config_update_functions(
			ADDR_OF(&agent->dp_config),
			ADDR_OF(&agent->cp_config),
			function_count,
			functions
		),
		"failed to update functions"
	);
}

int
agent_delete_function(struct agent *agent, const char *function_name) {
	return AGENT_TRY(
		agent,
		cp_config_delete_function(
			ADDR_OF(&agent->dp_config),
			ADDR_OF(&agent->cp_config),
			function_name
		),
		"failed to delete function"
	);
}

int
agent_update_pipelines(
	struct agent *agent,
	size_t pipeline_count,
	struct cp_pipeline_config *pipelines[]
) {
	return AGENT_TRY(
		agent,
		cp_config_update_pipelines(
			ADDR_OF(&agent->dp_config),
			ADDR_OF(&agent->cp_config),
			pipeline_count,
			pipelines
		),
		"failed to update pipelines"
	);
}

int
agent_delete_pipeline(struct agent *agent, const char *pipeline_name) {
	return AGENT_TRY(
		agent,
		cp_config_delete_pipeline(
			ADDR_OF(&agent->dp_config),
			ADDR_OF(&agent->cp_config),
			pipeline_name
		),
		"failed to delete pipeline"
	);
}

struct cp_pipeline_config *
cp_pipeline_config_create(const char *name, uint64_t length) {
	struct cp_pipeline_config *config = (struct cp_pipeline_config *)malloc(
		sizeof(struct cp_pipeline_config) +
		sizeof(char[CP_FUNCTION_NAME_LEN]) * length
	);
	if (config == NULL) {
		return NULL;
	}

	strtcpy(config->name, name, CP_PIPELINE_NAME_LEN);
	config->length = length;

	return config;
}

void
cp_pipeline_config_free(struct cp_pipeline_config *config) {
	free(config);
}

int
cp_pipeline_config_set_function(
	struct cp_pipeline_config *config, uint64_t index, const char *name
) {
	if (index >= config->length)
		return -1;
	strtcpy(config->functions[index], name, sizeof(config->functions[index])
	);
	return 0;
}

int
agent_update_devices(
	struct agent *agent, uint64_t device_count, struct cp_device *devices[]
) {
	return AGENT_TRY(
		agent,
		cp_config_update_devices(
			ADDR_OF(&agent->dp_config),
			ADDR_OF(&agent->cp_config),
			device_count,
			devices
		),
		"failed to update devices"
	);
}

int
yanet_get_dp_module_info(
	struct dp_module_list_info *module_list,
	uint64_t index,
	struct dp_module_info *module_info
) {
	if (index >= module_list->module_count)
		return -1;
	*module_info = module_list->modules[index];
	return 0;
}

void
dp_module_list_info_free(struct dp_module_list_info *module_list_info) {
	free(module_list_info);
}

struct dp_module_list_info *
yanet_get_dp_module_list_info(struct dp_config *dp_config) {
	dp_config_lock(dp_config);

	struct dp_module_list_info *module_list_info =
		(struct dp_module_list_info *)malloc(
			sizeof(struct dp_module_list_info) +
			dp_config->module_count * sizeof(struct dp_module_info)
		);
	if (module_list_info == NULL)
		goto unlock;

	struct dp_module *modules = ADDR_OF(&dp_config->dp_modules);

	module_list_info->module_count = dp_config->module_count;
	for (uint64_t module_idx = 0; module_idx < dp_config->module_count;
	     ++module_idx) {
		strtcpy(module_list_info->modules[module_idx].name,
			modules[module_idx].name,
			sizeof(module_list_info->modules[module_idx].name));
	}

unlock:
	dp_config_unlock(dp_config);

	return module_list_info;
}

// Modules

void
cp_module_list_info_free(struct cp_module_list_info *module_list_info) {
	free(module_list_info);
}

struct cp_module_list_info *
yanet_get_cp_module_list_info(struct dp_config *dp_config) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);

	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);
	struct cp_module_registry *module_registry =
		&config_gen->module_registry;

	struct cp_module_list_info *module_list_info =
		(struct cp_module_list_info *)malloc(
			sizeof(struct cp_module_list_info) +
			sizeof(struct cp_module_info) *
				module_registry->registry.capacity
		);
	if (module_list_info == NULL)
		goto unlock;

	module_list_info->module_count = 0;

	for (uint64_t module_idx = 0;
	     module_idx < module_registry->registry.capacity;
	     ++module_idx) {
		struct cp_module_info *info = module_list_info->modules +
					      module_list_info->module_count;

		struct cp_module *cp_module =
			cp_config_gen_get_module(config_gen, module_idx);
		if (cp_module == NULL) {
			continue;
		}
		strtcpy(info->type, cp_module->type, sizeof(info->type));
		strtcpy(info->name, cp_module->name, sizeof(info->name));
		info->gen = cp_module->gen;

		module_list_info->module_count += 1;
	}

unlock:
	cp_config_unlock(cp_config);

	return module_list_info;
}

struct cp_module_info *
yanet_get_cp_module_info(
	struct cp_module_list_info *module_list, uint64_t index
) {
	if (index >= module_list->module_count)
		return NULL;
	return module_list->modules + index;
}

// Functions

static void
cp_function_info_free(struct cp_function_info *function_info) {
	for (uint64_t idx = 0; idx < function_info->chain_count; ++idx) {
		free(function_info->chains[idx]);
	}

	free(function_info);
}

void
cp_function_list_info_free(struct cp_function_list_info *function_list_info) {
	for (uint64_t idx = 0; idx < function_list_info->function_count;
	     ++idx) {
		struct cp_function_info *function_info =
			function_list_info->functions[idx];
		cp_function_info_free(function_info);
	}

	free(function_list_info);
}

struct cp_function_list_info *
yanet_get_cp_function_list_info(struct dp_config *dp_config) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);

	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);
	struct cp_function_registry *function_registry =
		&config_gen->function_registry;

	struct cp_function_list_info *function_list_info =
		(struct cp_function_list_info *)malloc(
			sizeof(struct cp_function_list_info) +
			sizeof(struct cp_function_info *) *
				function_registry->registry.capacity
		);
	if (function_list_info == NULL)
		goto unlock;

	function_list_info->function_count = 0;

	for (uint64_t function_idx = 0;
	     function_idx < function_registry->registry.capacity;
	     ++function_idx) {
		struct cp_function *cp_function =
			cp_config_gen_get_function(config_gen, function_idx);
		if (cp_function == NULL) {
			continue;
		}

		struct cp_function_info *function_info =
			(struct cp_function_info *)malloc(
				sizeof(struct cp_function_info) +
				sizeof(struct cp_chain_info *) *
					cp_function->chain_count
			);

		strtcpy(function_info->name,
			cp_function->name,
			sizeof(function_info->name));
		function_info->chain_count = 0;

		for (uint64_t chain_idx = 0;
		     chain_idx < cp_function->chain_count;
		     ++chain_idx) {
			struct cp_function_chain *cp_function_chain =
				cp_function->chains + chain_idx;
			struct cp_chain *cp_chain =
				ADDR_OF(&cp_function_chain->cp_chain);

			struct cp_chain_info *chain_info =
				(struct cp_chain_info *)malloc(
					sizeof(struct cp_chain_info) +
					sizeof(struct cp_module_info_id) *
						cp_chain->length
				);
			if (chain_info == NULL) {
				goto error_free;
			}

			strtcpy(chain_info->name,
				cp_chain->name,
				sizeof(chain_info->name));
			chain_info->weight = cp_function_chain->weight;
			chain_info->length = cp_chain->length;
			for (uint64_t module_idx = 0;
			     module_idx < cp_chain->length;
			     ++module_idx) {
				strtcpy(chain_info->modules[module_idx].type,
					cp_chain->modules[module_idx].type,
					sizeof(chain_info->modules[module_idx]
						       .type));
				strtcpy(chain_info->modules[module_idx].name,
					cp_chain->modules[module_idx].name,
					sizeof(chain_info->modules[module_idx]
						       .name));
			}

			function_info->chains[chain_idx] = chain_info;
			function_info->chain_count += 1;
		}

		function_list_info
			->functions[function_list_info->function_count] =
			function_info;
		function_list_info->function_count += 1;
	}

	cp_config_unlock(cp_config);

	return function_list_info;

error_free:
	cp_function_list_info_free(function_list_info);
	function_list_info = NULL;

unlock:
	cp_config_unlock(cp_config);

	return function_list_info;
}

struct cp_function_info *
yanet_get_cp_function_info(
	struct cp_function_list_info *function_list, uint64_t index
) {
	if (index >= function_list->function_count)
		return NULL;

	return function_list->functions[index];
}

struct cp_chain_info *
yanet_get_cp_function_chain_info(
	struct cp_function_info *function_info, uint64_t index
) {
	if (index >= function_info->chain_count)
		return NULL;

	return function_info->chains[index];
}

struct cp_module_info_id *
yanet_get_cp_function_chain_module_info(
	struct cp_chain_info *chain_info, uint64_t index
) {
	if (index >= chain_info->length)
		return NULL;

	return chain_info->modules + index;
}

// Pipelines

void
cp_pipeline_list_info_free(struct cp_pipeline_list_info *pipeline_list_info) {
	for (uint64_t idx = 0; idx < pipeline_list_info->count; ++idx)
		free(pipeline_list_info->pipelines[idx]);
	free(pipeline_list_info);
}

struct cp_pipeline_list_info *
yanet_get_cp_pipeline_list_info(struct dp_config *dp_config) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);

	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);
	struct registry *pipeline_registry =
		&config_gen->pipeline_registry.registry;

	struct cp_pipeline_list_info *pipeline_list_info =
		(struct cp_pipeline_list_info *)malloc(
			sizeof(struct cp_pipeline_list_info) +
			sizeof(struct cp_pipeline_info *) *
				pipeline_registry->capacity
		);
	if (pipeline_list_info == NULL)
		goto unlock;

	memset(pipeline_list_info,
	       0,
	       sizeof(struct cp_pipeline_list_info) +
		       sizeof(struct cp_pipeline_info *) *
			       pipeline_registry->capacity);
	for (uint64_t idx = 0; idx < pipeline_registry->capacity; ++idx) {
		struct cp_pipeline *cp_pipeline =
			cp_config_gen_get_pipeline(config_gen, idx);
		if (cp_pipeline == NULL) {
			continue;
		}

		struct cp_pipeline_info *pipeline_info =
			(struct cp_pipeline_info *)malloc(
				sizeof(struct cp_pipeline_info) +
				sizeof(struct cp_function_info_id) *
					cp_pipeline->length
			);
		if (pipeline_info == NULL) {
			cp_pipeline_list_info_free(pipeline_list_info);
			pipeline_list_info = NULL;
			goto unlock;
		}

		strtcpy(pipeline_info->name,
			cp_pipeline->name,
			CP_PIPELINE_NAME_LEN);
		pipeline_info->length = cp_pipeline->length;
		for (uint64_t idx = 0; idx < cp_pipeline->length; ++idx) {
			strtcpy(pipeline_info->functions[idx].name,
				cp_pipeline->functions[idx].name,
				sizeof(pipeline_info->functions[idx].name));
		}
		pipeline_list_info->pipelines[pipeline_list_info->count++] =
			pipeline_info;
	}

unlock:
	cp_config_unlock(cp_config);

	return pipeline_list_info;
}

struct cp_pipeline_info *
yanet_get_cp_pipeline_info(
	struct cp_pipeline_list_info *pipeline_list_info, uint64_t index
) {
	if (index >= pipeline_list_info->count)
		return NULL;

	return pipeline_list_info->pipelines[index];
}

struct cp_function_info_id *
yanet_get_cp_pipeline_function_info_id(
	struct cp_pipeline_info *pipeline_info, uint64_t index
) {
	if (index >= pipeline_info->length)
		return NULL;

	return pipeline_info->functions + index;
}

// Devices

void
cp_device_list_info_free(struct cp_device_list_info *device_list_info) {
	for (uint64_t idx = 0; idx < device_list_info->device_count; ++idx) {
		free(device_list_info->devices[idx]);
	}

	free(device_list_info);
}

static struct cp_device_info *
yanet_build_device_info(struct cp_device *device) {
	struct cp_device_entry *input = ADDR_OF(&device->input_pipelines);
	struct cp_device_entry *output = ADDR_OF(&device->output_pipelines);

	struct cp_device_info *device_info = (struct cp_device_info *)malloc(
		sizeof(struct cp_device_info) +
		sizeof(struct cp_device_pipeline_info) *
			(input->pipeline_count + output->pipeline_count)
	);
	if (device_info == NULL) {
		return NULL;
	}

	strtcpy(device_info->type, device->type, CP_DEVICE_TYPE_LEN);
	strtcpy(device_info->name, device->name, CP_DEVICE_NAME_LEN);

	device_info->input_count = input->pipeline_count;
	device_info->output_count = output->pipeline_count;
	for (uint64_t idx = 0; idx < input->pipeline_count; ++idx) {
		strtcpy(device_info->pipelines[idx].name,
			input->pipelines[idx].name,
			sizeof(device_info->pipelines[idx].name));
		device_info->pipelines[idx].weight =
			input->pipelines[idx].weight;
	}

	for (uint64_t idx = 0; idx < output->pipeline_count; ++idx) {
		strtcpy(device_info->pipelines[device_info->input_count + idx]
				.name,
			output->pipelines[idx].name,
			sizeof(device_info
				       ->pipelines
					       [device_info->input_count + idx]
				       .name));
		device_info->pipelines[device_info->input_count + idx].weight =
			output->pipelines[idx].weight;
	}

	return device_info;
}

struct cp_device_list_info *
yanet_get_cp_device_list_info(struct dp_config *dp_config) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct cp_device_registry *device_registry =
		&cp_config_gen->device_registry;

	size_t device_list_info_size =
		sizeof(struct cp_device_list_info) +
		sizeof(struct cp_device_info *) *
			device_registry->registry.capacity;
	struct cp_device_list_info *device_list_info =
		(struct cp_device_list_info *)malloc(device_list_info_size);
	if (device_list_info == NULL)
		goto unlock;

	memset(device_list_info, 0, device_list_info_size);
	device_list_info->device_count = 0;
	for (uint64_t idx = 0; idx < device_registry->registry.capacity;
	     ++idx) {
		struct cp_device *cp_device =
			cp_config_gen_get_device(cp_config_gen, idx);
		if (cp_device == NULL) {
			continue;
		}
		struct cp_device_info *device_info =
			yanet_build_device_info(cp_device);
		if (device_info == NULL) {
			cp_device_list_info_free(device_list_info);
			device_list_info = NULL;
			goto unlock;
		}

		device_list_info->devices[device_list_info->device_count] =
			device_info;
		device_list_info->device_count++;
	}

unlock:
	cp_config_unlock(cp_config);

	return device_list_info;
}

struct cp_device_info *
yanet_get_cp_device_info(
	struct cp_device_list_info *device_list_info, uint64_t idx
) {
	if (idx >= device_list_info->device_count)
		return NULL;

	return device_list_info->devices[idx];
}

struct cp_device_pipeline_info *
yanet_get_cp_device_input_pipeline_info(
	struct cp_device_info *device_info, uint64_t idx
) {
	if (idx >= device_info->input_count)
		return NULL;

	return device_info->pipelines + idx;
}

struct cp_device_pipeline_info *
yanet_get_cp_device_output_pipeline_info(
	struct cp_device_info *device_info, uint64_t idx
) {
	if (idx >= device_info->output_count)
		return NULL;

	return device_info->pipelines + device_info->input_count + idx;
}

int
yanet_get_cp_agent_instance_info(
	struct cp_agent_info *agent_info,
	uint64_t index,
	struct cp_agent_instance_info **instance_info
) {
	if (index >= agent_info->instance_count) {
		errno = ERANGE;
		return -1;
	}

	*instance_info = agent_info->instances + index;

	return 0;
}

int
yanet_get_cp_agent_info(
	struct cp_agent_list_info *agent_list_info,
	uint64_t index,
	struct cp_agent_info **agent_info
) {
	if (index >= agent_list_info->count) {
		errno = ERANGE;
		return -1;
	}
	*agent_info = agent_list_info->agents[index];
	return 0;
}

void
cp_agent_list_info_free(struct cp_agent_list_info *agent_list_info) {
	for (uint64_t agent_idx = 0; agent_idx < agent_list_info->count;
	     ++agent_idx) {
		free(agent_list_info->agents[agent_idx]);
	}

	free(agent_list_info);
}

struct cp_agent_list_info *
yanet_get_cp_agent_list_info(struct dp_config *dp_config) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);

	struct cp_agent_registry *agent_registry =
		ADDR_OF(&cp_config->agent_registry);

	struct cp_agent_list_info *agent_list_info =
		(struct cp_agent_list_info *)malloc(
			sizeof(struct cp_agent_list_info) +
			sizeof(struct cp_agent_info *) * agent_registry->count
		);
	if (agent_list_info == NULL) {
		goto unlock;
	}
	agent_list_info->count = 0;

	for (uint64_t agent_idx = 0; agent_idx < agent_registry->count;
	     ++agent_idx) {
		struct agent *agent =
			ADDR_OF(&agent_registry->agents[agent_idx]);
		uint64_t instance_count = 1;
		struct agent *prev_agent = ADDR_OF(&agent->prev);
		while (prev_agent != NULL) {
			prev_agent = ADDR_OF(&prev_agent->prev);
			++instance_count;
		}

		struct cp_agent_info *agent_info = (struct cp_agent_info *)
			malloc(sizeof(struct cp_agent_info) +
			       sizeof(struct cp_agent_instance_info) *
				       instance_count);
		if (agent_info == NULL) {
			cp_agent_list_info_free(agent_list_info);
			agent_list_info = NULL;
			goto unlock;
		}

		strtcpy(agent_info->name, agent->name, sizeof(agent_info->name)
		);
		agent_info->instance_count = 0;
		while (agent_info->instance_count < instance_count) {
			struct cp_agent_instance_info *instance =
				agent_info->instances +
				agent_info->instance_count++;
			instance->pid = agent->pid;
			instance->memory_limit = agent->memory_limit;
			instance->allocated = agent->memory_context.balloc_size;
			instance->freed = agent->memory_context.bfree_size;
			instance->gen = agent->gen;
			agent = ADDR_OF(&agent->prev);
		}

		agent_list_info->agents[agent_list_info->count++] = agent_info;
	}

unlock:
	cp_config_unlock(cp_config);
	return agent_list_info;
}

struct cp_device_config *
cp_device_config_create(
	const char *name,
	uint64_t input_pipeline_count,
	uint64_t output_pipeline_count
) {
	struct cp_device_config *config =
		(struct cp_device_config *)malloc(sizeof(struct cp_device_config
		));

	if (config == NULL)
		return NULL;

	memset(config, 0, sizeof(struct cp_device_config));
	strtcpy(config->name, name, CP_DEVICE_NAME_LEN);
	config->input_pipelines = (struct cp_device_entry_config *)malloc(
		sizeof(struct cp_device_entry_config) +
		sizeof(struct cp_pipeline_weight_config) * input_pipeline_count
	);
	if (config->input_pipelines == NULL) {
		cp_device_config_free(config);
		return NULL;
	}
	memset(config->input_pipelines,
	       0,
	       sizeof(struct cp_device_entry_config) +
		       sizeof(struct cp_pipeline_weight_config) *
			       input_pipeline_count);
	config->input_pipelines->count = input_pipeline_count;

	config->output_pipelines = (struct cp_device_entry_config *)malloc(
		sizeof(struct cp_device_entry_config) +
		sizeof(struct cp_pipeline_weight_config) * output_pipeline_count
	);
	if (config->output_pipelines == NULL) {
		cp_device_config_free(config);
		return NULL;
	}
	memset(config->output_pipelines,
	       0,
	       sizeof(struct cp_device_entry_config) +
		       sizeof(struct cp_pipeline_weight_config) *
			       output_pipeline_count);
	config->output_pipelines->count = output_pipeline_count;

	return config;
}

void
cp_device_config_free(struct cp_device_config *config) {
	free(config->output_pipelines);
	free(config->input_pipelines);
	free(config);
}

int
cp_device_config_set_input_pipeline(
	struct cp_device_config *device,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	if (index >= device->input_pipelines->count)
		return -1;
	strtcpy(device->input_pipelines->pipelines[index].name,
		name,
		CP_PIPELINE_NAME_LEN);
	device->input_pipelines->pipelines[index].weight = weight;

	return 0;
}

int
cp_device_config_set_output_pipeline(
	struct cp_device_config *device,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	if (index >= device->output_pipelines->count)
		return -1;
	strtcpy(device->output_pipelines->pipelines[index].name,
		name,
		CP_PIPELINE_NAME_LEN);
	device->output_pipelines->pipelines[index].weight = weight;

	return 0;
}

struct counter_handle_list *
yanet_get_module_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name,
	const char *module_type,
	const char *module_name
) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct counter_registry *counter_registry;
	struct counter_storage *counter_storage;

	struct counter_storage *cs = cp_config_gen_get_module_counter_storage(
		cp_config_gen,
		device_name,
		pipeline_name,
		function_name,
		chain_name,
		module_type,
		module_name
	);

	if (cs == NULL) {
		cp_config_unlock(cp_config);
		return NULL;
	}
	counter_storage = cs;
	counter_registry = ADDR_OF(&counter_storage->registry);

	uint64_t count = counter_registry->count;
	struct counter_name *names = ADDR_OF(&counter_registry->names);

	// FIXME: unlock is correct
	cp_config_unlock(cp_config);

	struct counter_handle_list *list = (struct counter_handle_list *)malloc(
		sizeof(struct counter_handle_list) +
		sizeof(struct counter_handle) * count
	);

	if (list == NULL)
		return NULL;
	list->instance_count =
		ADDR_OF(&counter_storage->allocator)->instance_count;
	list->count = count;
	struct counter_handle *handlers = list->counters;

	for (uint64_t idx = 0; idx < count; ++idx) {
		strtcpy(handlers[idx].name, names[idx].name, 60);
		handlers[idx].size = names[idx].size;
		handlers[idx].gen = names[idx].gen;
		handlers[idx].value_handle =
			counter_get_value_handle(idx, counter_storage);
	}

	return list;
}

struct counter_handle_list *
yanet_get_chain_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name,
	const char *chain_name
) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct counter_registry *counter_registry;
	struct counter_storage *counter_storage;

	struct counter_storage *cs = cp_config_gen_get_chain_counter_storage(
		cp_config_gen,
		device_name,
		pipeline_name,
		function_name,
		chain_name
	);

	if (cs == NULL) {
		cp_config_unlock(cp_config);
		return NULL;
	}
	counter_storage = cs;
	counter_registry = ADDR_OF(&counter_storage->registry);

	uint64_t count = counter_registry->count;
	struct counter_name *names = ADDR_OF(&counter_registry->names);

	// FIXME: unlock is correct
	cp_config_unlock(cp_config);

	struct counter_handle_list *list = (struct counter_handle_list *)malloc(
		sizeof(struct counter_handle_list) +
		sizeof(struct counter_handle) * count
	);

	if (list == NULL)
		return NULL;
	list->instance_count =
		ADDR_OF(&counter_storage->allocator)->instance_count;
	list->count = count;
	struct counter_handle *handlers = list->counters;

	for (uint64_t idx = 0; idx < count; ++idx) {
		strtcpy(handlers[idx].name, names[idx].name, 60);
		handlers[idx].size = names[idx].size;
		handlers[idx].gen = names[idx].gen;
		handlers[idx].value_handle =
			counter_get_value_handle(idx, counter_storage);
	}

	return list;
}

struct counter_handle_list *
yanet_get_function_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name,
	const char *function_name
) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct counter_registry *counter_registry;
	struct counter_storage *counter_storage;

	struct counter_storage *cs = cp_config_gen_get_function_counter_storage(
		cp_config_gen, device_name, pipeline_name, function_name
	);

	if (cs == NULL) {
		cp_config_unlock(cp_config);
		return NULL;
	}
	counter_storage = cs;
	counter_registry = ADDR_OF(&counter_storage->registry);

	uint64_t count = counter_registry->count;
	struct counter_name *names = ADDR_OF(&counter_registry->names);

	// FIXME: unlock is correct
	cp_config_unlock(cp_config);

	struct counter_handle_list *list = (struct counter_handle_list *)malloc(
		sizeof(struct counter_handle_list) +
		sizeof(struct counter_handle) * count
	);

	if (list == NULL)
		return NULL;
	list->instance_count =
		ADDR_OF(&counter_storage->allocator)->instance_count;
	list->count = count;
	struct counter_handle *handlers = list->counters;

	for (uint64_t idx = 0; idx < count; ++idx) {
		strtcpy(handlers[idx].name, names[idx].name, 60);
		handlers[idx].size = names[idx].size;
		handlers[idx].gen = names[idx].gen;
		handlers[idx].value_handle =
			counter_get_value_handle(idx, counter_storage);
	}

	return list;
}

struct counter_handle_list *
yanet_get_pipeline_counters(
	struct dp_config *dp_config,
	const char *device_name,
	const char *pipeline_name
) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct counter_registry *counter_registry;
	struct counter_storage *counter_storage;

	struct counter_storage *cs = cp_config_gen_get_pipeline_counter_storage(
		cp_config_gen, device_name, pipeline_name
	);

	if (cs == NULL) {
		cp_config_unlock(cp_config);
		return NULL;
	}
	counter_storage = cs;
	counter_registry = ADDR_OF(&counter_storage->registry);

	uint64_t count = counter_registry->count;
	struct counter_name *names = ADDR_OF(&counter_registry->names);

	// FIXME: unlock is correct
	cp_config_unlock(cp_config);

	struct counter_handle_list *list = (struct counter_handle_list *)malloc(
		sizeof(struct counter_handle_list) +
		sizeof(struct counter_handle) * count
	);

	if (list == NULL)
		return NULL;
	list->instance_count =
		ADDR_OF(&counter_storage->allocator)->instance_count;
	list->count = count;
	struct counter_handle *handlers = list->counters;

	for (uint64_t idx = 0; idx < count; ++idx) {
		strtcpy(handlers[idx].name, names[idx].name, 60);
		handlers[idx].size = names[idx].size;
		handlers[idx].gen = names[idx].gen;
		handlers[idx].value_handle =
			counter_get_value_handle(idx, counter_storage);
	}

	return list;
}

struct counter_handle_list *
yanet_get_device_counters(
	struct dp_config *dp_config, const char *device_name
) {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);
	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&cp_config->cp_config_gen);

	struct counter_registry *counter_registry;
	struct counter_storage *counter_storage;

	struct counter_storage *cs = cp_config_gen_get_device_counter_storage(
		cp_config_gen, device_name
	);

	if (cs == NULL) {
		cp_config_unlock(cp_config);
		return NULL;
	}
	counter_storage = cs;
	counter_registry = ADDR_OF(&counter_storage->registry);

	uint64_t count = counter_registry->count;
	struct counter_name *names = ADDR_OF(&counter_registry->names);

	// FIXME: unlock is correct
	cp_config_unlock(cp_config);

	struct counter_handle_list *list = (struct counter_handle_list *)malloc(
		sizeof(struct counter_handle_list) +
		sizeof(struct counter_handle) * count
	);

	if (list == NULL)
		return NULL;
	list->instance_count =
		ADDR_OF(&counter_storage->allocator)->instance_count;
	list->count = count;
	struct counter_handle *handlers = list->counters;

	for (uint64_t idx = 0; idx < count; ++idx) {
		strtcpy(handlers[idx].name, names[idx].name, 60);
		handlers[idx].size = names[idx].size;
		handlers[idx].gen = names[idx].gen;
		handlers[idx].value_handle =
			counter_get_value_handle(idx, counter_storage);
	}

	return list;
}

struct counter_handle *
yanet_get_counter(struct counter_handle_list *counters, uint64_t idx) {
	if (idx >= counters->count)
		return NULL;
	return counters->counters + idx;
}

uint64_t
yanet_get_counter_value(
	struct counter_value_handle *value_handle,
	uint64_t value_idx,
	uint64_t worker_idx
) {
	return counter_handle_get_value(value_handle, worker_idx)[value_idx];
}

struct counter_handle_list *
yanet_get_worker_counters(struct dp_config *dp_config) {
	struct counter_registry *counter_registry = &dp_config->worker_counters;
	struct counter_storage *storage =
		ADDR_OF(&dp_config->worker_counter_storage);

	uint64_t count = counter_registry->count;
	struct counter_name *names = ADDR_OF(&counter_registry->names);

	struct counter_handle_list *list = (struct counter_handle_list *)malloc(
		sizeof(struct counter_handle_list) +
		sizeof(struct counter_handle) * count
	);

	if (list == NULL)
		return NULL;
	list->instance_count = ADDR_OF(&storage->allocator)->instance_count;
	list->count = count;
	struct counter_handle *handlers = list->counters;

	for (uint64_t idx = 0; idx < count; ++idx) {
		strtcpy(handlers[idx].name, names[idx].name, 60);
		handlers[idx].size = names[idx].size;
		handlers[idx].gen = names[idx].gen;
		handlers[idx].value_handle =
			counter_get_value_handle(idx, storage);
	}

	return list;
}

void
yanet_counter_handle_list_free(struct counter_handle_list *counters) {
	free(counters);
}

void
agent_free_unused_agents(struct agent *agent) {
	if (agent == NULL) {
		return;
	}
	agent = ADDR_OF(&agent->prev);
	while (agent != NULL) {
		struct agent *prev_agent = ADDR_OF(&agent->prev);
		if (agent->loaded_module_count == 0) {
			agent_cleanup(agent);
		}
		agent = prev_agent;
	}
}

struct dp_config *
agent_dp_config(struct agent *agent) {
	return ADDR_OF(&agent->dp_config);
}

const char *
agent_take_error(struct agent *agent) {
	return diag_take_msg(&agent->diag);
}

void
agent_clean_error(struct agent *agent) {
	diag_reset(&agent->diag);
}
