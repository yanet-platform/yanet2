#include "agent.h"

#include <linux/mman.h>
#include <sys/mman.h>
#include <sys/stat.h>

#include <fcntl.h>
#include <unistd.h>

#include "common/memory.h"

#include "dataplane/config/zone.h"

#include "api/agent.h"

struct dp_config *
yanet_attach(const char *storage_name) {

	int mem_fd = open(storage_name, O_RDWR, S_IRUSR | S_IWUSR);
	struct stat stat;
	fstat(mem_fd, &stat);

	void *storage =
		mmap(NULL,
		     stat.st_size,
		     PROT_READ | PROT_WRITE,
		     MAP_SHARED,
		     mem_fd,
		     0);
	close(mem_fd);

	struct dp_config *dp_config = (struct dp_config *)storage;
	return dp_config;
}

struct agent *
agent_connect(
	const char *storage_name, const char *agent_name, size_t memory_limit
) {

	int mem_fd = open(storage_name, O_RDWR, S_IRUSR | S_IWUSR);
	if (mem_fd == -1) {
		return NULL;
	}
	struct stat stat;
	fstat(mem_fd, &stat);

	void *storage =
		mmap(NULL,
		     stat.st_size,
		     PROT_READ | PROT_WRITE,
		     MAP_SHARED,
		     mem_fd,
		     0);
	close(mem_fd);
	if (storage == MAP_FAILED) {
		return NULL;
	}

	struct dp_config *dp_config = (struct dp_config *)storage;
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);

	struct agent *new_agent = (struct agent *)memory_balloc(
		&cp_config->memory_context, sizeof(struct agent)
	);
	strncpy(new_agent->name, agent_name, 80);
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
	while (memory_limit > 0) {
		size_t alloc_size = memory_limit;
		if (alloc_size > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE) {
			alloc_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE;
		}
		void *alloc =
			memory_balloc(&cp_config->memory_context, alloc_size);
		block_allocator_put_arena(
			&new_agent->block_allocator, alloc, alloc_size
		);

		memory_limit -= alloc_size;
	}

	SET_OFFSET_OF(&new_agent->dp_config, dp_config);
	SET_OFFSET_OF(&new_agent->cp_config, cp_config);
	new_agent->pid = getpid();

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

		SET_OFFSET_OF(&new_registry->prev, old_registry);

		SET_OFFSET_OF(&cp_config->agent_registry, new_registry);
	}

	return new_agent;
}

void
agent_disconnect(struct agent *agent) {
	void *storage = ADDR_OF(&agent->dp_config);

	munmap(storage, ADDR_OF(&agent->dp_config)->storage_size);
}

int
agent_update_modules(
	struct agent *agent,
	size_t module_count,
	struct module_data **module_datas
) {
	return cp_config_update_modules(
		ADDR_OF(&agent->cp_config), module_count, module_datas
	);
}

int
agent_update_pipelines(
	struct agent *agent,
	size_t pipeline_count,
	struct pipeline_config *pipelines[]
) {
	return cp_config_update_pipelines(
		ADDR_OF(&agent->dp_config),
		ADDR_OF(&agent->cp_config),
		pipeline_count,
		pipelines
	);
}

struct pipeline_config *
pipeline_config_create(uint64_t length) {
	struct pipeline_config *config = (struct pipeline_config *)malloc(
		sizeof(struct pipeline_config) +
		sizeof(struct module_config) * length
	);

	config->length = length;

	return config;
}

void
pipeline_config_free(struct pipeline_config *config) {
	free(config);
}

void
pipeline_config_set_module(
	struct pipeline_config *config,
	uint64_t index,
	const char *type,
	const char *name
) {
	strncpy(config->modules[index].type,
		type,
		sizeof(config->modules[index].type));
	strncpy(config->modules[index].name,
		name,
		sizeof(config->modules[index].name));
}

int
agent_update_devices(
	struct agent *agent, uint64_t device_count, uint64_t *pipelines
) {
	return cp_config_update_devices(
		ADDR_OF(&agent->dp_config),
		ADDR_OF(&agent->cp_config),
		device_count,
		pipelines
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
		strncpy(module_list_info->modules[module_idx].name,
			modules[module_idx].name,
			80);
	}

unlock:
	dp_config_unlock(dp_config);

	return module_list_info;
}

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
		ADDR_OF(&config_gen->module_registry);

	struct cp_module_list_info *module_list_info =
		(struct cp_module_list_info *)malloc(
			sizeof(struct cp_module_list_info) +
			sizeof(struct cp_module_info) * module_registry->count
		);
	if (module_list_info == NULL)
		goto unlock;

	module_list_info->gen = config_gen->gen;
	module_list_info->module_count = module_registry->count;
	for (uint64_t module_idx = 0; module_idx < module_registry->count;
	     ++module_idx) {
		struct module_data *module_data =
			ADDR_OF(&(module_registry->modules + module_idx)->data);
		module_list_info->modules[module_idx].index =
			module_data->index;
		strncpy(module_list_info->modules[module_idx].config_name,
			module_data->name,
			80);
	}

unlock:
	cp_config_unlock(cp_config);

	return module_list_info;
}

int
yanet_get_cp_module_info(
	struct cp_module_list_info *module_list,
	uint64_t index,
	struct cp_module_info *module_info
) {
	if (index >= module_list->module_count)
		return -1;
	*module_info = module_list->modules[index];
	return 0;
}

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
	struct cp_pipeline_registry *pipeline_registry =
		ADDR_OF(&config_gen->pipeline_registry);

	struct cp_pipeline_list_info *pipeline_list_info =
		(struct cp_pipeline_list_info *)malloc(
			sizeof(struct cp_pipeline_list_info) +
			sizeof(struct cp_pipeline_info *) *
				pipeline_registry->count
		);
	if (pipeline_list_info == NULL)
		goto unlock;

	memset(pipeline_list_info,
	       0,
	       sizeof(struct cp_pipeline_list_info
	       ) + sizeof(struct cp_pipeline_info *) * pipeline_registry->count
	);
	pipeline_list_info->count = pipeline_registry->count;
	for (uint64_t idx = 0; idx < pipeline_registry->count; ++idx) {
		struct cp_pipeline *cp_pipeline =
			pipeline_registry->pipelines + idx;
		struct cp_pipeline_info *pipeline_info =
			(struct cp_pipeline_info *)malloc(
				sizeof(struct cp_pipeline_info) +
				sizeof(uint64_t) * cp_pipeline->length
			);
		if (pipeline_info == NULL) {
			cp_pipeline_list_info_free(pipeline_list_info);
			pipeline_list_info = NULL;
			goto unlock;
		}
		pipeline_info->length = cp_pipeline->length;
		memcpy(pipeline_info->modules,
		       ADDR_OF(&cp_pipeline->module_indexes),
		       sizeof(uint64_t) * cp_pipeline->length);

		pipeline_list_info->pipelines[idx] = pipeline_info;
	}

unlock:
	cp_config_unlock(cp_config);

	return pipeline_list_info;
}

int
yanet_get_cp_pipeline_info(
	struct cp_pipeline_list_info *pipeline_list_info,
	uint64_t index,
	struct cp_pipeline_info **pipeline_info
) {
	if (index >= pipeline_list_info->count)
		return -1;

	*pipeline_info = pipeline_list_info->pipelines[index];

	return 0;
}

int
yanet_get_cp_pipeline_module_info(
	struct cp_pipeline_info *pipeline_info,
	uint64_t index,
	uint64_t *config_index
) {
	if (index >= pipeline_info->length)
		return -1;

	*config_index = pipeline_info->modules[index];
	return 0;
}

struct cp_agent_list_info {};

/*
struct cp_agent_list_info
yanet_get_agent_list {
	struct dp_config *dp_config
} {
	struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
	cp_config_lock(cp_config);

	struct cp_agent_list_info *agent_list_info = (struct cp_agent_list_info
*) malloc(sizeof)


unlock:
	cp_config_unlock(cp_config);
	return agent_list_info;
}
*/
