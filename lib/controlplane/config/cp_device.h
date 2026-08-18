#pragma once

#include "common/memory.h"

#include "lib/counters/counters.h"

#include "lib/controlplane/config/defines.h"

#include "lib/controlplane/config/registry.h"

#include "lib/errors/errors.h"

struct agent;
struct cp_device;

// Free handler for a device subclass, run when the agent reclaims a parked
// device of that subclass's type.
//
// It must release the subclass's own allocations, then call cp_device_fini
// and free the enclosing subclass allocation.
typedef void (*cp_device_free_handler)(struct cp_device *self);

struct cp_device_pipeline {
	char name[CP_PIPELINE_NAME_LEN];
	uint64_t weight;
};

struct cp_device_entry {
	uint64_t counter_packet_rx;
	uint64_t counter_packet_entry;
	uint64_t counter_packet_tx;
	uint64_t counter_packet_drop;
	uint64_t counter_packet_pending_input;
	uint64_t counter_packet_pending_output;
	uint64_t pipeline_count;
	struct cp_device_pipeline pipelines[];
};

struct cp_device {
	struct registry_item config_item;
	char type[80];
	char name[CP_DEVICE_NAME_LEN];

	uint64_t dp_device_idx;

	// Agent that owns this device. Set by cp_device_init; used by the
	// typed free routine to resolve the memory context for reclamation.
	struct agent *agent;

	struct memory_context memory_context;

	struct counter_registry counter_registry;

	struct cp_device_entry *input_pipelines;
	struct cp_device_entry *output_pipelines;

	// Link to the next device parked on the same agent's list, once this
	// device's reference count reaches zero.
	//
	// Only the zero-transition handler sets it, and only a later reclaim
	// for this device's own type reads it. It stays unset until that
	// transition happens. The parked list's tail refers to itself
	// instead of ending at null, so a parked entry's link is never null
	// — which also marks that this device is already parked.
	struct cp_device *parked_next;
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

// Tear down the base resources acquired by cp_device_init.
//
// Base only: a subclass frees its own allocations in its own typed free
// routine before calling this. Safe to call from an init-failure rollback.
// Idempotent on zero-init.
void
cp_device_fini(struct cp_device *self);

// Destroy every parked device of one type, using the caller's destructor
// for that type.
//
// A different type sharing the same parked list is left in place for its
// own next call. A parked entry already sits at reference count zero, so
// the destructor runs directly with no further release and nothing here
// outlives this call. A device type's construction call runs this before
// allocating, so a recreation under memory pressure benefits from the
// space a parked instance would free rather than failing before reaching
// that reclaim. The caller must not hold the configuration lock.
void
cp_device_drain_parked(
	struct agent *agent,
	const char *device_type,
	cp_device_free_handler destroy
);

// The single handler for a device reference count reaching zero, reached
// the same way from a registry drop or an explicit release.
//
// Parks the device on its agent's list instead of destroying it, because
// this generic layer does not know the device's subclass, and an agent can
// host more than one device type at once. The device's own type-specific
// reclaim destroys it later.
//
// Idempotent: once set, a parked entry's link is never null again, so a
// duplicate transition leaves it in place instead of relinking it. Every
// caller already holds the configuration lock, so this handler must not
// take it itself.
void
cp_device_registry_item_free_cb(struct registry_item *item, void *data);

// Drop the reference construction took on the caller's behalf.
//
// The zero-transition handler runs only when this drop is the last
// reference, so a caller must not assume the call freed anything: a live
// or pinned configuration generation may still hold the device. A device
// parked here is not destroyed on the spot — the next construction call
// for the same type reclaims it, or it is freed along with the agent's
// arena.
//
// Takes the device's own agent's configuration lock itself, unlike the
// registry-driven path to the same handler, which already runs under
// that lock.
void
cp_device_release(struct cp_device *self);

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
	struct cp_device_registry *device_registry,
	const char *type,
	const char *name
);

int
cp_device_registry_upsert(
	struct cp_device_registry *device_registry,
	const char *type,
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
