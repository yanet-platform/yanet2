#include "agent.h"

#include <linux/mman.h>
#include <sys/mman.h>
#include <sys/stat.h>

#include <fcntl.h>
#include <unistd.h>

#include "common/memory.h"

#include "dataplane/config/zone.h"

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
	struct cp_config *cp_config = ADDR_OF(dp_config, dp_config->cp_config);

	/*
	 * FIXME: the code bellow tries to allocate memory_limit bytes
	 * using max possible chunk size what breaks allocator encapsulation.
	 * Alternative multi-alloc api should be implemented.
	 */
	size_t alloc_size = memory_limit;
	if (alloc_size > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE) {
		alloc_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE;
	}
	memory_limit -= alloc_size;
	struct agent *agent = (struct agent *)memory_balloc(
		&cp_config->memory_context, alloc_size
	);
	block_allocator_init(&agent->block_allocator);
	block_allocator_put_arena(
		&agent->block_allocator,
		agent + 1,
		alloc_size - sizeof(struct agent)
	);
	memory_context_init(
		&agent->memory_context, agent_name, &agent->block_allocator
	);

	while (memory_limit > 0) {
		alloc_size = memory_limit;
		if (alloc_size > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE) {
			alloc_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE;
		}
		void *alloc =
			memory_balloc(&cp_config->memory_context, alloc_size);
		block_allocator_put_arena(
			&agent->block_allocator, alloc, alloc_size
		);

		memory_limit -= alloc_size;
	}

	agent->dp_config = dp_config;
	agent->cp_config = cp_config;

	return agent;
}

int
agent_update_modules(
	struct agent *agent,
	size_t module_count,
	struct module_data **module_datas
) {
	return cp_config_update_modules(
		agent->cp_config, module_count, module_datas
	);
}

int
agent_update_pipelines(
	struct agent *agent,
	size_t pipeline_count,
	struct pipeline_config *pipelines[]
) {
	return cp_config_update_pipelines(
		agent->dp_config, agent->cp_config, pipeline_count, pipelines
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
