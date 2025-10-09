#pragma once

#include "common/memory.h"

#include "counters/counters.h"

#include "controlplane/config/defines.h"

#include "controlplane/config/registry.h"

struct cp_device_pipeline {
	char name[CP_PIPELINE_NAME_LEN];
	uint64_t weight;
};

struct cp_device_entry {
	uint64_t pipeline_count;
	struct cp_device_pipeline pipelines[];
};

struct cp_device {
	struct registry_item config_item;
	char name[CP_DEVICE_NAME_LEN];

	uint32_t type;

	struct counter_registry counter_registry;

	struct cp_device_entry *input_pipelines;
	struct cp_device_entry *output_pipelines;
};

struct dp_config;
struct cp_config_gen;

struct cp_pipeline_weight_config {
	char name[CP_PIPELINE_NAME_LEN];
	uint64_t weight;
};

struct cp_device_entry_config {
	uint64_t count;
	struct cp_pipeline_weight_config pipelines[];
};

struct cp_device_config {
	char name[CP_DEVICE_NAME_LEN];
	uint64_t type;
	struct cp_device_entry_config *input_pipelines;
	struct cp_device_entry_config *output_pipelines;
};

struct cp_device *
cp_device_create(
	struct memory_context *memory_context,
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	struct cp_device_config *device_config
);

void
cp_device_free(struct memory_context *memory_context, struct cp_device *device);

/*
 * Pipeline registry contains all existing devices.
 * After reading a packet a dataplane worker evaluates index of a
 * device assigned to process the packet and fetchs device descriptor
 * from the device registry insdide active configuration generation.
 */

struct cp_device_registry {
	struct memory_context *memory_context;
	struct registry registry;
};

int
cp_device_registry_init(
	struct memory_context *memory_context,
	struct cp_device_registry *registry
);

int
cp_device_registry_copy(
	struct memory_context *memory_context,
	struct cp_device_registry *new_device_registry,
	struct cp_device_registry *old_device_registry
);

void
cp_device_registry_destroy(struct cp_device_registry *device_registry);

struct cp_device *
cp_device_registry_get(
	struct cp_device_registry *device_registry, uint64_t idx
);

struct cp_device *
cp_device_registry_lookup(
	struct cp_device_registry *device_registry, const char *name
);

int
cp_device_registry_upsert(
	struct cp_device_registry *device_registry,
	const char *name,
	struct cp_device *device
);

int
cp_device_registry_delete(
	struct cp_device_registry *device_registry, const char *name
);

static inline uint64_t
cp_device_registry_capacity(struct cp_device_registry *device_registry) {
	return device_registry->registry.capacity;
}
