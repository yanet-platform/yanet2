#include "mock.h"

#include "common/memory_address.h"
#include "common/memory_block.h"
#include "lib/dataplane/config/zone.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct mock {
	uint8_t *arena;
	size_t arena_size;
	size_t used;
	bool should_free;
	struct dp_config dp_config;
	struct dp_module module;
	struct dp_worker worker;
};

struct mock *
mock_init(void *arena, size_t memory) {
	if (memory < sizeof(struct mock)) {
		return NULL;
	}
	struct mock *mock = (struct mock *)arena;
	mock->should_free = false;
	memset(&mock->dp_config, 0, sizeof(struct dp_config));
	mock->dp_config.module_count = 1;
	SET_OFFSET_OF(&mock->dp_config.dp_modules, &mock->module);
	sprintf(mock->module.name, "balancer");
	mock->dp_config.instance_count = 1;
	mock->dp_config.instance_idx = 0;
	mock->dp_config.worker_count = 1;
	mock->arena = arena;
	mock->arena_size = memory;
	mock->used = sizeof(struct mock);
	memset(&mock->worker, 0, sizeof(struct dp_worker));
	mock->worker.idx = 0;
	return mock;
}

struct mock *
mock_create(size_t memory) {
	void *arena = malloc(memory);
	struct mock *mock = mock_init(arena, memory);
	if (mock == NULL) {
		free(mock);
		return NULL;
	} else {
		mock->should_free = true;
		return mock;
	}
}

void
mock_free(struct mock *mock) {
	if (mock->should_free) {
		free(mock->arena);
	}
}

struct agent *
mock_create_agent(struct mock *mock, size_t memory) {
	if (mock->used + sizeof(struct agent) + memory > mock->arena_size) {
		return NULL;
	}
	struct agent *agent = (struct agent *)(mock->arena + mock->used);
	memset(agent, 0, sizeof(*agent));
	SET_OFFSET_OF(&agent->dp_config, &mock->dp_config);
	int res = block_allocator_init(&agent->block_allocator);
	if (res < 0) {
		return NULL;
	}
	block_allocator_put_arena(
		&agent->block_allocator,
		mock->arena + mock->used + sizeof(struct agent),
		memory
	);
	res = memory_context_init(
		&agent->memory_context, "mock_agent", &agent->block_allocator
	);
	if (res < 0) {
		return NULL;
	}
	sprintf(agent->name, "balancer");
	agent->memory_limit = memory;
	mock->used += sizeof(struct agent) + memory;
	return agent;
}