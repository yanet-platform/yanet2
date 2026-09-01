#include "agent.h"

#include <assert.h>
#include <linux/mman.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>

#include <fcntl.h>
#include <unistd.h>

#include <errno.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"
#include "common/spinlock.h"
#include "common/strutils.h"

#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/errors/errors.h"

#include "api/agent.h"
#include "api/info.h"

#include <stdio.h>

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

	size_t size = (size_t)stat.st_size;
	void *ptr = mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
	close(fd);
	if (ptr == MAP_FAILED) {
		return NULL;
	}

	struct yanet_shm *shm = malloc(sizeof(*shm));
	if (shm == NULL) {
		int saved_errno = errno;
		munmap(ptr, size);
		errno = saved_errno;
		return NULL;
	}
	shm->base = ptr;
	shm->size = size;
	return shm;
}

int
yanet_shm_detach(struct yanet_shm *shm) {
	// The handle is released whatever the unmap reports: a caller has no
	// way to retry with a better length, so keeping the handle alive
	// would only turn a leaked mapping into a dangling pointer as well.
	int rc = munmap(shm->base, shm->size);
	free(shm);
	return rc;
}

struct dp_config *
yanet_shm_dp_config(struct yanet_shm *shm, uint32_t instance_idx) {
	return dp_config_nextk((struct dp_config *)shm->base, instance_idx);
}

int
agent_dp_config_ready(struct yanet_shm *shm, uint32_t instance_idx) {
	uint32_t count = __atomic_load_n(
		&((struct dp_config *)shm->base)->instance_count,
		__ATOMIC_ACQUIRE
	);
	if (instance_idx >= count) {
		return 0;
	}
	struct dp_config *dp_config = yanet_shm_dp_config(shm, instance_idx);
	return __atomic_load_n(&dp_config->ready_magic, __ATOMIC_ACQUIRE) ==
	       DP_CONFIG_READY_MAGIC;
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

int
dataplane_instance_current_time(
	struct dp_config *dp_config, uint64_t *time_ns
) {
	if (dp_config == NULL || time_ns == NULL) {
		return -1;
	}

	uint64_t worker_count = dp_config->worker_count;
	struct dp_worker **workers = ADDR_OF(&dp_config->workers);
	if (workers == NULL) {
		return -1;
	}

	uint64_t latest = 0;
	for (uint64_t idx = 0; idx < worker_count; ++idx) {
		struct dp_worker *worker = ADDR_OF(&workers[idx]);
		if (worker == NULL) {
			continue;
		}

		uint64_t published = __atomic_load_n(
			&worker->current_time, __ATOMIC_RELAXED
		);
		if (published > latest) {
			latest = published;
		}
	}

	if (latest == 0) {
		return -1;
	}

	*time_ns = latest;
	return 0;
}

// Body of agent_free_unused_agents for a caller that already holds
// cp_config_lock. See the definition further down for details.
static void
agent_free_unused_agents_locked(struct agent *agent);

static int
allocate_arenas(
	struct memory_context *memory_context,
	struct agent_arena *arenas,
	size_t arena_count,
	uint64_t size,
	yanet_error **err
) {
	(void)arena_count;
	size_t added = 0;
	uint64_t left = size;
	while (left > 0) {
		assert(added < arena_count);

		uint64_t arena_size = left > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE
					      ? MEMORY_BLOCK_ALLOCATOR_MAX_SIZE
					      : left;

		void *arena = memory_balloc(memory_context, arena_size);
		if (arena == NULL) {
			yanet_error_add(
				err, "failed to allocate memory for arena"
			);
			for (uint64_t idx = 0; idx < added; ++idx) {
				struct agent_arena *reserved = &arenas[idx];
				memory_bfree(
					memory_context,
					ADDR_OF(&reserved->data),
					reserved->size
				);
			}
			return -1;
		}

		SET_OFFSET_OF(&arenas[added].data, arena);
		arenas[added].size = arena_size;
		++added;

		left -= arena_size;
	}

	return 0;
}

static size_t
calculate_arena_count(uint64_t size) {
	/*
	 * FIXME: the code bellow tries to allocate memory_limit bytes
	 * using max possible chunk size what breaks allocator encapsulation.
	 * Alternative multi-alloc api should be implemented.
	 */
	if (size == 0) {
		return 0;
	}
	return (size - 1) / MEMORY_BLOCK_ALLOCATOR_MAX_SIZE + 1;
}

struct agent *
agent_attach(
	struct yanet_shm *shm,
	uint32_t instance_idx,
	const char *agent_name,
	size_t memory_limit,
	yanet_error **err
) {
	struct dp_config *dp_config = yanet_shm_dp_config(shm, instance_idx);

	// Guard against attaching before the dataplane finishes initialising
	// the shared memory instance. A zeroed segment has ready_magic == 0,
	// so ADDR_OF(&dp_config->cp_config) would yield NULL and the subsequent
	// cp_config_lock call would fault. Acquire ordering pairs with the
	// release store in dp_config_mark_ready, published once the dataplane
	// has released cp_config, guaranteeing the cp_config offset pointer is
	// fully visible once we pass this check.
	uint64_t magic =
		__atomic_load_n(&dp_config->ready_magic, __ATOMIC_ACQUIRE);
	if (magic != DP_CONFIG_READY_MAGIC) {
		yanet_error_add(
			err,
			"dataplane shared memory instance %u is not yet "
			"initialised",
			instance_idx
		);
		return NULL;
	}

	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);

	cp_config_lock(cp_config);

	struct agent *new_agent = (struct agent *)memory_balloc(
		&cp_config->memory_context, sizeof(struct agent)
	);
	if (new_agent == NULL) {
		yanet_error_add(err, "failed to allocate memory for agent");
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

	uint64_t arena_count = calculate_arena_count(memory_limit);
	struct agent_arena *arenas = (struct agent_arena *)memory_balloc(
		&cp_config->memory_context,
		sizeof(struct agent_arena) * arena_count
	);
	if (arenas == NULL) {
		yanet_error_add(err, "failed to allocate memory for arenas");
		agent_cleanup(new_agent);
		new_agent = NULL;
		goto unlock;
	}

	memset(arenas, 0, sizeof(struct agent_arena) * arena_count);

	if (allocate_arenas(
		    &cp_config->memory_context,
		    arenas,
		    arena_count,
		    memory_limit,
		    err
	    ) != 0) {
		memory_bfree(
			&cp_config->memory_context,
			arenas,
			sizeof(struct agent_arena) * arena_count
		);
		agent_cleanup(new_agent);
		new_agent = NULL;
		goto unlock;
	}

	SET_OFFSET_OF(&new_agent->arenas, arenas);
	new_agent->arena_count = arena_count;

	for (uint64_t arena_idx = 0; arena_idx < arena_count; ++arena_idx) {
		block_allocator_put_arena(
			&new_agent->block_allocator,
			ADDR_OF(&arenas[arena_idx].data),
			arenas[arena_idx].size
		);
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
			yanet_error_add(
				err,
				"failed to allocate memory for agent registry"
			);
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

	agent_free_unused_agents_locked(new_agent);

unlock:
	cp_config_unlock(cp_config);

	return new_agent;
}

uint64_t
agent_memory_limit(struct agent *agent) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	cp_config_lock(cp_config);
	uint64_t memory_limit = agent->memory_limit;
	cp_config_unlock(cp_config);

	return memory_limit;
}

int
agent_extend(struct agent *agent, uint64_t size, yanet_error **err) {
	int ret = 0;

	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct memory_context *cp_memory_context = &cp_config->memory_context;

	cp_config_lock(cp_config);

	// An agent that draws straight from the controlplane pool owns no
	// arena of its own and cannot be grown here.
	//
	// Built-in services are set up that way.
	if (ADDR_OF(&agent->memory_context.block_allocator) !=
	    &agent->block_allocator) {
		yanet_error_add(
			err,
			"agent \"%s\" draws memory from the controlplane "
			"pool and cannot be extended",
			agent->name
		);
		ret = -1;
		goto unlock;
	}

	uint64_t needed = size;
	uint64_t misaligned = needed % MEMORY_BLOCK_ALLOCATOR_MIN_SIZE;
	if (misaligned != 0) {
		uint64_t pad = MEMORY_BLOCK_ALLOCATOR_MIN_SIZE - misaligned;
		if (needed > UINT64_MAX - pad) {
			yanet_error_add(
				err, "agent cannot grow by %lu bytes", size
			);
			ret = -1;
			goto unlock;
		}
		needed += pad;
	}
	if (needed == 0) {
		goto unlock;
	}

	struct agent_arena *arenas = ADDR_OF(&agent->arenas);
	uint64_t arena_count = agent->arena_count;

	uint64_t added_count = calculate_arena_count(needed);
	uint64_t new_arena_count = arena_count + added_count;

	struct agent_arena *new_arenas = (struct agent_arena *)memory_balloc(
		cp_memory_context, sizeof(struct agent_arena) * new_arena_count
	);
	if (new_arenas == NULL) {
		yanet_error_add(err, "failed to allocate memory for arenas");
		ret = -1;
		goto unlock;
	}
	memset(new_arenas, 0, sizeof(struct agent_arena) * new_arena_count);

	if (allocate_arenas(
		    cp_memory_context,
		    new_arenas + arena_count,
		    added_count,
		    needed,
		    err
	    ) != 0) {
		memory_bfree(
			cp_memory_context,
			new_arenas,
			sizeof(struct agent_arena) * new_arena_count
		);
		ret = -1;
		goto unlock;
	}

	for (uint64_t arena_idx = 0; arena_idx < arena_count; ++arena_idx) {
		EQUATE_OFFSET(
			&new_arenas[arena_idx].data, &arenas[arena_idx].data
		);
		new_arenas[arena_idx].size = arenas[arena_idx].size;
	}

	SET_OFFSET_OF(&agent->arenas, new_arenas);
	agent->arena_count = new_arena_count;
	agent->memory_limit += needed;

	for (uint64_t idx = 0; idx < added_count; ++idx) {
		struct agent_arena *reserved = &new_arenas[arena_count + idx];
		block_allocator_put_arena(
			&agent->block_allocator,
			ADDR_OF(&reserved->data),
			reserved->size
		);
	}

	memory_bfree(
		cp_memory_context,
		arenas,
		sizeof(struct agent_arena) * arena_count
	);

unlock:
	cp_config_unlock(cp_config);

	return ret;
}

void
agent_cleanup(struct agent *agent) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	// Finalize the agent context before freeing the arenas that back any
	// child contexts (e.g. module contexts), so the detach walk in
	// memory_context_fini never touches freed arena memory.
	memory_context_fini(&agent->memory_context);

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

	struct agent_storage *storage = ADDR_OF(&agent->storage);
	while (storage != NULL) {
		struct agent_storage *next = ADDR_OF(&storage->next);
		memory_bfree(
			&cp_config->memory_context,
			storage,
			sizeof(struct agent_storage) + storage->size
		);
		storage = next;
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
	struct agent *agent,
	size_t module_count,
	struct cp_module **modules,
	yanet_error **err
) {
	int ret = cp_config_update_modules(
		ADDR_OF(&agent->dp_config),
		ADDR_OF(&agent->cp_config),
		module_count,
		modules,
		err
	);

	if (ret != 0) {
		return -1;
	}

	agent_free_unused_agents(agent);

	return 0;
}

int
agent_delete_module(
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	yanet_error **err
) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	int ret = cp_config_delete_module(
		dp_config, cp_config, module_type, module_name, err
	);

	if (ret != 0) {
		return -1;
	}

	agent_free_unused_agents(agent);

	return 0;
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
	if (cp_chain_config == NULL) {
		return NULL;
	}

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
	if (config == NULL) {
		return NULL;
	}
	strtcpy(config->name, name, CP_FUNCTION_NAME_LEN);
	config->chain_count = chain_count;

	return config;
}

void
cp_function_config_free(struct cp_function_config *config) {
	for (uint64_t idx = 0; idx < config->chain_count; ++idx) {
		if (config->chains[idx].chain != NULL) {
			cp_chain_config_free(config->chains[idx].chain);
		}
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
	if (index >= cp_function_config->chain_count) {
		return -1;
	}

	if (cp_function_config->chains[index].chain != NULL) {
		return -1;
	}

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
	struct cp_function_config *functions[],
	yanet_error **err
) {
	int ret = cp_config_update_functions(
		ADDR_OF(&agent->dp_config),
		ADDR_OF(&agent->cp_config),
		function_count,
		functions,
		err
	);

	if (ret != 0) {
		return -1;
	}

	return 0;
}

int
agent_delete_function(
	struct agent *agent, const char *function_name, yanet_error **err
) {
	int ret = cp_config_delete_function(
		ADDR_OF(&agent->dp_config),
		ADDR_OF(&agent->cp_config),
		function_name,
		err
	);

	if (ret != 0) {
		return -1;
	}

	return 0;
}

int
agent_update_pipelines(
	struct agent *agent,
	size_t pipeline_count,
	struct cp_pipeline_config *pipelines[],
	yanet_error **err
) {
	int ret = cp_config_update_pipelines(
		ADDR_OF(&agent->dp_config),
		ADDR_OF(&agent->cp_config),
		pipeline_count,
		pipelines,
		err
	);

	if (ret != 0) {
		return -1;
	}

	return 0;
}

int
agent_delete_pipeline(
	struct agent *agent, const char *pipeline_name, yanet_error **err
) {
	int ret = cp_config_delete_pipeline(
		ADDR_OF(&agent->dp_config),
		ADDR_OF(&agent->cp_config),
		pipeline_name,
		err
	);

	if (ret != 0) {
		return -1;
	}

	return 0;
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
	if (index >= config->length) {
		return -1;
	}
	strtcpy(config->functions[index], name, sizeof(config->functions[index])
	);
	return 0;
}

int
agent_update_devices(
	struct agent *agent,
	uint64_t device_count,
	struct cp_device *devices[],
	yanet_error **err
) {
	int ret = cp_config_update_devices(
		ADDR_OF(&agent->dp_config),
		ADDR_OF(&agent->cp_config),
		device_count,
		devices,
		err
	);

	if (ret != 0) {
		return -1;
	}

	agent_free_unused_agents(agent);

	return 0;
}

int
agent_delete_device(
	struct agent *agent, const char *device_name, yanet_error **err
) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	int ret =
		cp_config_delete_device(dp_config, cp_config, device_name, err);

	if (ret != 0) {
		return -1;
	}

	agent_free_unused_agents(agent);

	return 0;
}

int
agent_update_objects(
	struct agent *agent,
	uint64_t object_count,
	struct cp_object *objects[],
	yanet_error **err
) {
	int ret = cp_config_update_objects(
		ADDR_OF(&agent->dp_config),
		ADDR_OF(&agent->cp_config),
		object_count,
		objects,
		err
	);

	if (ret != 0) {
		return -1;
	}

	agent_free_unused_agents(agent);

	return 0;
}

int
agent_delete_object(
	struct agent *agent,
	const char *object_type,
	const char *object_name,
	yanet_error **err
) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	int ret = cp_config_delete_object(
		dp_config, cp_config, object_type, object_name, err
	);

	if (ret != 0) {
		return -1;
	}

	agent_free_unused_agents(agent);

	return 0;
}

int
yanet_get_dp_module_info(
	struct dp_module_list_info *module_list,
	uint64_t index,
	struct dp_module_info *module_info
) {
	if (index >= module_list->module_count) {
		return -1;
	}
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
	if (module_list_info == NULL) {
		goto unlock;
	}

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
	if (module_list_info == NULL) {
		goto unlock;
	}

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
	if (index >= module_list->module_count) {
		return NULL;
	}
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
	if (function_list_info == NULL) {
		goto unlock;
	}

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
		if (function_info == NULL) {
			goto error_free;
		}

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
				cp_function_info_free(function_info);
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
	if (index >= function_list->function_count) {
		return NULL;
	}

	return function_list->functions[index];
}

struct cp_chain_info *
yanet_get_cp_function_chain_info(
	struct cp_function_info *function_info, uint64_t index
) {
	if (index >= function_info->chain_count) {
		return NULL;
	}

	return function_info->chains[index];
}

struct cp_module_info_id *
yanet_get_cp_function_chain_module_info(
	struct cp_chain_info *chain_info, uint64_t index
) {
	if (index >= chain_info->length) {
		return NULL;
	}

	return chain_info->modules + index;
}

// Pipelines

void
cp_pipeline_list_info_free(struct cp_pipeline_list_info *pipeline_list_info) {
	for (uint64_t idx = 0; idx < pipeline_list_info->count; ++idx) {
		free(pipeline_list_info->pipelines[idx]);
	}
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
	if (pipeline_list_info == NULL) {
		goto unlock;
	}

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
	if (index >= pipeline_list_info->count) {
		return NULL;
	}

	return pipeline_list_info->pipelines[index];
}

struct cp_function_info_id *
yanet_get_cp_pipeline_function_info_id(
	struct cp_pipeline_info *pipeline_info, uint64_t index
) {
	if (index >= pipeline_info->length) {
		return NULL;
	}

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
yanet_build_device_info(struct cp_device *device, uint64_t index) {
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
	device_info->index = index;

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
	if (device_list_info == NULL) {
		goto unlock;
	}

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
			yanet_build_device_info(cp_device, idx);
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
	if (idx >= device_list_info->device_count) {
		return NULL;
	}

	return device_list_info->devices[idx];
}

struct cp_device_pipeline_info *
yanet_get_cp_device_input_pipeline_info(
	struct cp_device_info *device_info, uint64_t idx
) {
	if (idx >= device_info->input_count) {
		return NULL;
	}

	return device_info->pipelines + idx;
}

struct cp_device_pipeline_info *
yanet_get_cp_device_output_pipeline_info(
	struct cp_device_info *device_info, uint64_t idx
) {
	if (idx >= device_info->output_count) {
		return NULL;
	}

	return device_info->pipelines + device_info->input_count + idx;
}

// Count the nodes of a memory_context subtree, depth-first.
static uint64_t
memory_context_count_nodes(struct memory_context *ctx) {
	uint64_t count = 1;
	struct memory_context *child = ADDR_OF(&ctx->first_child);
	while (child != NULL) {
		count += memory_context_count_nodes(child);
		child = ADDR_OF(&child->next_sibling);
	}
	return count;
}

// Snapshot a memory_context subtree into nodes, depth-first, capped at
// capacity entries.
static void
memory_context_fill_nodes(
	struct memory_context *ctx,
	struct cp_memory_node_info *nodes,
	uint32_t parent_idx,
	uint32_t *next_idx,
	uint32_t capacity
) {
	if (*next_idx >= capacity) {
		return;
	}
	uint32_t my_idx = (*next_idx)++;
	struct cp_memory_node_info *node = &nodes[my_idx];
	strtcpy(node->name, ctx->name, sizeof(node->name));
	node->parent_idx = parent_idx;
	node->_pad = 0;
	node->balloc_count =
		__atomic_load_n(&ctx->balloc_count, __ATOMIC_RELAXED);
	node->bfree_count =
		__atomic_load_n(&ctx->bfree_count, __ATOMIC_RELAXED);
	node->balloc_size =
		__atomic_load_n(&ctx->balloc_size, __ATOMIC_RELAXED);
	node->bfree_size = __atomic_load_n(&ctx->bfree_size, __ATOMIC_RELAXED);

	struct memory_context *child = ADDR_OF(&ctx->first_child);
	while (child != NULL) {
		struct memory_context *sibling = ADDR_OF(&child->next_sibling);
		memory_context_fill_nodes(
			child, nodes, my_idx, next_idx, capacity
		);
		child = sibling;
	}
}

// Build the heap-side instance info for one agent, including its
// memory-context tree snapshot.
//
// The tree is a two-phase best-effort read: count under lock, malloc outside
// it, then fill under lock again bounded by the counted capacity. Config
// compilation is now parallel, so the tree can legitimately change between
// the two passes -- a node appearing or vanishing is not a bug, it is the
// documented best-effort semantics of memory_node_count.
static struct cp_agent_instance_info *
build_instance_info(struct agent *agent) {
	// The allocator backing agent->memory_context also guards the
	// context-tree splices (see the lock's own comment in
	// common/memory_block.h). For agent_attach'd agents that allocator is
	// the agent's own block_allocator; for dp_system_agent_new agents the
	// context is rooted in cp_config's context instead, so its tree is
	// guarded by cp_config's allocator lock. Reading the lock off the
	// context itself covers both cases uniformly.
	struct block_allocator *alloc =
		ADDR_OF(&agent->memory_context.block_allocator);

	spinlock_lock(&alloc->lock);
	uint64_t node_count =
		memory_context_count_nodes(&agent->memory_context);
	spinlock_unlock(&alloc->lock);

	struct cp_agent_instance_info *info = (struct cp_agent_instance_info *)
		malloc(sizeof(struct cp_agent_instance_info) +
		       sizeof(struct cp_memory_node_info) * node_count);
	if (info == NULL) {
		return NULL;
	}
	info->pid = agent->pid;
	info->memory_limit = agent->memory_limit;
	info->gen = agent->gen;
	// For agent_attach'd agents, &agent->block_allocator is the same
	// allocator as alloc above, and its spinlock is not reentrant, so this
	// call must run strictly outside the locked sections above and below.
	// For dp_system_agent_new agents the two allocators differ, but running
	// it here uniformly keeps the call site the same for both shapes.
	info->free_bytes = block_allocator_free_size(&agent->block_allocator);

	uint32_t next_idx = 0;
	spinlock_lock(&alloc->lock);
	memory_context_fill_nodes(
		&agent->memory_context,
		info->memory_nodes,
		UINT32_MAX,
		&next_idx,
		(uint32_t)node_count
	);
	spinlock_unlock(&alloc->lock);
	info->memory_node_count = next_idx;
	return info;
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

	*instance_info = agent_info->instances[index];

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
	if (agent_list_info == NULL) {
		return;
	}
	for (uint64_t agent_idx = 0; agent_idx < agent_list_info->count;
	     ++agent_idx) {
		struct cp_agent_info *agent_info =
			agent_list_info->agents[agent_idx];
		if (agent_info == NULL) {
			continue;
		}
		for (uint64_t inst_idx = 0;
		     inst_idx < agent_info->instance_count;
		     ++inst_idx) {
			free(agent_info->instances[inst_idx]);
		}
		free(agent_info);
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
			       sizeof(struct cp_agent_instance_info *) *
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
				build_instance_info(agent);
			if (instance == NULL) {
				for (uint64_t k = 0;
				     k < agent_info->instance_count;
				     ++k) {
					free(agent_info->instances[k]);
				}
				free(agent_info);
				cp_agent_list_info_free(agent_list_info);
				agent_list_info = NULL;
				goto unlock;
			}
			agent_info->instances[agent_info->instance_count++] =
				instance;

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

	if (config == NULL) {
		return NULL;
	}

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
	cp_device_config_fini(config);
	free(config);
}

int
cp_device_config_set_input_pipeline(
	struct cp_device_config *device,
	uint64_t index,
	const char *name,
	uint64_t weight
) {
	if (index >= device->input_pipelines->count) {
		return -1;
	}
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
	if (index >= device->output_pipelines->count) {
		return -1;
	}
	strtcpy(device->output_pipelines->pipelines[index].name,
		name,
		CP_PIPELINE_NAME_LEN);
	device->output_pipelines->pipelines[index].weight = weight;

	return 0;
}

// Unlocked body shared by agent_free_unused_agents and the agent_attach call
// site, which already holds cp_config_lock itself.
//
// Depends on a precondition it does not itself enforce: within one shared
// memory instance, a live agent name has at most one process still holding
// it. Only a reference kind that cannot outlive its owning process may gate
// this reclaim, since nothing here can tell a dead owner from a live one
// across a restart. A second live holder of the same name sharing that gate
// would instead have its arena freed out from under it. Mechanically
// proving this precondition is tracked separately as issue #2001.
static void
agent_free_unused_agents_locked(struct agent *agent) {
	if (agent == NULL) {
		return;
	}

	while (ADDR_OF(&agent->prev) != NULL) {
		struct agent *prev_agent = ADDR_OF(&agent->prev);

		if (prev_agent->loaded_module_count == 0 &&
		    prev_agent->loaded_device_count == 0 &&
		    prev_agent->loaded_object_count == 0) {
			SET_OFFSET_OF(&agent->prev, ADDR_OF(&prev_agent->prev));
			agent_cleanup(prev_agent);
			continue;
		}

		agent = ADDR_OF(&agent->prev);
	}
}

void
agent_free_unused_agents(struct agent *agent) {
	if (agent == NULL) {
		return;
	}

	// This walks and splices the agent->prev chain and calls agent_cleanup,
	// which frees the superseded agent into cp_config->memory_context. It
	// must run under cp_config_lock now that Go-side callers are no longer
	// serialized by a global mutex.
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	cp_config_lock(cp_config);
	agent_free_unused_agents_locked(agent);
	cp_config_unlock(cp_config);
}

struct dp_config *
agent_dp_config(struct agent *agent) {
	return ADDR_OF(&agent->dp_config);
}

void *
agent_storage_read(struct agent *agent, const char *name) {
	struct agent_storage *storage = ADDR_OF(&agent->storage);
	while (storage != NULL) {
		if (strncmp(storage->name, name, 80) == 0) {
			return storage->data;
		}
		storage = ADDR_OF(&storage->next);
	}
	return NULL;
}

int
agent_storage_put(
	struct agent *agent,
	const char *name,
	void *data,
	size_t size,
	yanet_error **err
) {
	struct agent_storage *storage = ADDR_OF(&agent->storage);
	struct agent_storage *prev = NULL;
	struct memory_context *mctx = &agent->memory_context;

	struct agent_storage *new_storage =
		memory_balloc(mctx, sizeof(struct agent_storage) + size);
	if (new_storage == NULL) {
		yanet_error_add(err, "memory not enough");
		return -1;
	}

	memcpy(new_storage->data, data, size);
	strtcpy(new_storage->name, name, sizeof(new_storage->name));
	new_storage->size = size;
	new_storage->next = NULL;

	while (storage != NULL) {
		struct agent_storage *next = ADDR_OF(&storage->next);
		if (strncmp(storage->name, name, 80) == 0) {
			memory_bfree(
				mctx,
				storage,
				sizeof(struct agent_storage) + storage->size
			);
			SET_OFFSET_OF(&new_storage->next, next);
			goto set_prev;
		}
		prev = storage;
		storage = next;
	}

set_prev:
	if (prev != NULL) {
		SET_OFFSET_OF(&prev->next, new_storage);
	} else {
		SET_OFFSET_OF(&agent->storage, new_storage);
	}

	return 0;
}
