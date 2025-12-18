#pragma once

#include <assert.h>
#include <netinet/in.h>
#include <stddef.h>
#include <stdint.h>

#include "array.h"
#include "index.h"

////////////////////////////////////////////////////////////////////////////////

struct service_registry {
	struct service_array array;

	struct service_index index;

	struct memory_context *mctx;
};

int
service_registry_init(
	struct service_registry *registry, struct memory_context *mctx
);

void
service_registry_free(struct service_registry *registry);

////////////////////////////////////////////////////////////////////////////////

ssize_t
service_registry_find_or_insert_service(
	struct service_registry *registry,
	uint8_t *vip_address,
	int vip_proto,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto,
	struct service_info **result
);

struct service_info *
service_registry_lookup(struct service_registry *registry, size_t idx);