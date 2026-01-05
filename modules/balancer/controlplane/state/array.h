#pragma once

#include "common/memory.h"
#include "service.h"

#include "common/memory_block.h"

////////////////////////////////////////////////////////////////////////////////

#define SERVICE_REGISTRY_BLOCK_SIZE (4096)

static_assert(
	sizeof(union service_state) * SERVICE_REGISTRY_BLOCK_SIZE <=
		MEMORY_BLOCK_ALLOCATOR_MAX_SIZE,
	"too big block"
);

////////////////////////////////////////////////////////////////////////////////

struct service_array_block {
	union service_state services[SERVICE_REGISTRY_BLOCK_SIZE];
};

struct service_array {
	size_t size;
	struct service_array_block **blocks;
	struct memory_context mctx;
};

union service_state *
service_array_lookup(struct service_array *array, size_t idx);

void
service_array_init(struct service_array *array, struct memory_context *mctx);

void
service_array_free(struct service_array *array);

int
service_array_push_back(
	struct service_array *array, union service_state *state
);