#pragma once

#include "service.h"

#include "common/memory_block.h"

////////////////////////////////////////////////////////////////////////////////

#define SERVICE_REGISTRY_BLOCK_SIZE (4096)

static_assert(
	sizeof(struct service_info) * SERVICE_REGISTRY_BLOCK_SIZE <=
		MEMORY_BLOCK_ALLOCATOR_MAX_SIZE,
	"too big block"
);

////////////////////////////////////////////////////////////////////////////////

struct service_array_block {
	struct service_info services[SERVICE_REGISTRY_BLOCK_SIZE];
};

struct service_array {
	size_t size;
	struct service_array_block **blocks;
	struct memory_context *mctx;
};

struct service_info *
service_array_lookup(struct service_array *array, size_t idx);

void
service_array_init(struct service_array *array, struct memory_context *mctx);

void
service_array_free(struct service_array *array);

int
service_array_push_back(
	struct service_array *array,
	uint8_t *vip_address,
	int vip_proto,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto
);