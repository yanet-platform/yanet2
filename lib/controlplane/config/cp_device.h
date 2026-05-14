#pragma once

#include "common/memory.h"

#include "counters/counters.h"

#include "controlplane/config/defines.h"

#include "controlplane/config/registry.h"

#include "lib/errors/errors.h"

struct agent;

struct cp_device_pipeline {
	char name[CP_PIPELINE_NAME_LEN];
	uint64_t weight;
};

struct cp_device_entry {
	uint64_t pipeline_count;
	struct cp_device_pipeline pipelines[];
};

struct cp_device {
	// Offset pointer to the memory_context used to allocate this struct.
	//
	// Set by cp_device_new, or by a subclass create function before
	// cp_device_init.
	//
	// Consumed by cp_device_free to reclaim the allocation.
	struct memory_context *parent_memory_context;

	// Number of bytes to pass to memory_bfree when freeing this struct.
	//
	// Set by cp_device_new OR by a subclass create function before
	// cp_device_init.
	//
	// Subclasses MUST store sizeof their wrapper struct here so that
	// cp_device_free reclaims the proper full allocation.
	uint64_t alloc_size;

	struct registry_item config_item;
	char type[80];
	char name[CP_DEVICE_NAME_LEN];

	uint64_t dp_device_idx;

	struct agent *agent;

	struct memory_context memory_context;

	struct counter_registry counter_registry;

	struct cp_device_entry *input_pipelines;
	struct cp_device_entry *output_pipelines;

	uint64_t counter_packet_rx_count;
	uint64_t counter_packet_tx_count;
	uint64_t counter_packet_rx_bytes;
	uint64_t counter_packet_tx_bytes;
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
	char type[80];
	struct cp_device_entry_config *input_pipelines;
	struct cp_device_entry_config *output_pipelines;
};

int
cp_device_config_init(
	struct cp_device_config *cp_device_config,
	const char *type,
	const char *name,
	uint64_t input_pipeline_count,
	uint64_t output_pipeline_count,
	yanet_error **err
);

// Release the input/output pipeline arrays embedded in config.
//
// Leaves the config struct itself untouched: the caller decides whether to
// free it or hand it back to its enclosing storage.
void
cp_device_config_fini(struct cp_device_config *config);

// Allocate a new cp_device from mctx.
//
// Returns NULL on allocation failure; caller is responsible for reporting the
// error.
struct cp_device *
cp_device_new(struct memory_context *mctx);

// Release the memory backing self.
//
// NULL-safe no-op.
//
// Does not call cp_device_fini: caller must do that separately first.
void
cp_device_free(struct cp_device *self);

// Initialize device resources: sub-context, pipelines, counter registry.
//
// On failure, internally calls cp_device_fini and returns -1.
int
cp_device_init(
	struct cp_device *self,
	struct agent *agent,
	const struct cp_device_config *cfg,
	yanet_error **err
);

// Tear down resources acquired by cp_device_init.
//
// Idempotent on zero-init.
void
cp_device_fini(struct cp_device *self);

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
	struct cp_device_registry *registry,
	yanet_error **err
);

int
cp_device_registry_copy(
	struct memory_context *memory_context,
	struct cp_device_registry *new_device_registry,
	struct cp_device_registry *old_device_registry,
	yanet_error **err
);

void
cp_device_registry_fini(struct cp_device_registry *device_registry);

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
	struct cp_device *device,
	yanet_error **err
);

int
cp_device_registry_delete(
	struct cp_device_registry *device_registry, const char *name
);

static inline uint64_t
cp_device_registry_capacity(struct cp_device_registry *device_registry) {
	return device_registry->registry.capacity;
}
