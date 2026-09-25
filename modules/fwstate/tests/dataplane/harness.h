#pragma once

#include <stdbool.h> // IWYU pragma: export
#include <stdint.h>  // IWYU pragma: export
#include <stdlib.h>  // IWYU pragma: export

#include "common/memory.h" // IWYU pragma: export
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/time/clock.h"
#include "lib/errors/errors.h"	// IWYU pragma: export
#include "lib/fwstate/config.h" // IWYU pragma: export
#include "lib/fwstate/stash.h"
#include "lib/fwstate/types.h"	// IWYU pragma: export
#include "lib/statemap/fwmap.h" // IWYU pragma: export
#include "modules/fwstate/api/fwstate_cp.h"
#include "modules/fwstate/dataplane/config.h" // IWYU pragma: export
#include "modules/fwstate/dataplane/dataplane.h"
#include "objects/fwstate/api/fwstate_map_v4_object.h"
#include "objects/fwstate/api/fwstate_map_v6_object.h"

// Forward declaration of fwstate_handle_packets from the dataplane module.
void
fwstate_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

// Allocate and zero-initialize a stand-in agent inside the given memory
// context, ready to pass to a module's own control-plane constructor.
//
// Returns NULL on allocation failure.
struct agent *
fwstate_test_agent_new(struct memory_context *parent, const char *name);

// Link the module's counter registry and spawn a per-worker counter storage.
//
// The dataplane resolves per-worker counter addresses via
// counter_get_address(), which dereferences module_ectx.counter_storage.
// Reproduce the real dataplane setup once at config time and reuse the storage
// across handle calls so that counter values accumulate across invocations.
//
// Returns NULL on failure (the error chain is freed internally).
struct counter_storage *
fwstate_test_counter_storage_setup(struct cp_module *cp_module);

// Free a counter storage allocated by fwstate_test_counter_storage_setup.
// Safe to call with NULL.
void
fwstate_test_counter_storage_free(struct counter_storage *storage);

// Execution contexts per module config and worker the harness can stand
// in for, as one config placed at several points of a pipeline has.
#define FWSTATE_TEST_CONTEXT_COUNT 2

// Return the harness-owned prepared buffer of one execution context of a
// module config on one worker, zeroed on first use, or NULL past the
// harness limits or when its table is full.
struct fwstate_prepared *
fwstate_test_prepared(
	struct cp_module *cp_module, uint16_t context, uint16_t worker_idx
);

// Forget the prepared buffers of a module config, so a config allocated at
// the address of an earlier one does not inherit its stale stash links.
void
fwstate_test_prepared_reset(struct cp_module *cp_module);

// Test wrapper for fwstate_handle_packets that runs the given one of the
// config's stand-in execution contexts on the worker. It constructs the
// module context from cp_module and a pre-spawned counter storage, wiring
// the module's declared object links through the agent's object registry
// the way the production execution-context build does. The storage is
// owned by the caller and reused across calls so counters accumulate.
void
test_fwstate_handle_packets_in_context(
	struct dp_worker *dp_worker,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front,
	uint16_t context
);

// Workers every harness map stash and module config's per-worker read
// positions are sized for.
#define FWSTATE_TEST_WORKER_COUNT 2

// Return the stand-in worker of the given index: a valid round counter and
// a mock mbuf pool for emitted sync packets. Workers persist for the whole
// process. Returns NULL for an index at or past FWSTATE_TEST_WORKER_COUNT.
struct dp_worker *
fwstate_test_worker(uint16_t worker_idx);

// Number of mock-pool mbufs the harness workers hold outside the pool.
uint64_t
fwstate_test_pool_outstanding(struct dp_worker *dp_worker);

// Let the harness workers' mock pool hand out only extra more mbufs than
// it currently has outstanding; every later allocation past that fails.
void
fwstate_test_pool_limit(struct dp_worker *dp_worker, uint32_t extra);

// Restore the default capacity of the harness workers' mock pool.
void
fwstate_test_pool_unlimit(struct dp_worker *dp_worker);

// Start a new round on the worker, as the worker loop does before every
// packet pass.
void
fwstate_test_next_round(struct dp_worker *dp_worker);

// Append a pending record to the worker's stash slot of the map linked for
// the frame's family, as ACL does. Returns 0, or -1 when the family has no
// linked map or the slot is full.
int
fwstate_test_stash_push(
	struct cp_module *cp_module,
	struct dp_worker *dp_worker,
	const struct fw_state_sync_frame *frame,
	uint16_t rx_device_id,
	uint16_t tx_device_id
);

// Return the worker's stash slot of the map linked for one family, or
// NULL without a linked map.
struct fwstate_stash_slot *
fwstate_test_stash_slot(
	struct cp_module *cp_module, bool is_ipv6, uint16_t worker_idx
);

// Read one value of a module counter of worker storage by counter name,
// or UINT64_MAX when the module registers no such counter.
uint64_t
fwstate_test_counter(
	struct cp_module *cp_module,
	struct counter_storage *storage,
	const char *name,
	uint64_t value_idx
);

// Return one record of a stash slot, resolving the slot's buffer offset
// pointer, which cgo cannot follow.
static inline struct fwstate_sync_record *
fwstate_test_slot_record(struct fwstate_stash_slot *slot, uint32_t idx) {
	return fwstate_stash_slot_records(slot) + idx;
}

// Sum the free bytes of the block allocator behind the agent's memory
// context: a full create-and-destroy cycle that frees everything leaves
// the sum unchanged.
uint64_t
fwstate_test_free_bytes(struct agent *agent);

// Mark every queued packet as emitted by a local fwstate.
void
fwstate_test_mark_internal(struct packet_front *packet_front);

// Helper to get actual pointer from an offset pointer.
void *
addr_of(void **field);

// Create a standalone fwstate-map object of the requested family with a
// stash of stash_size bytes per worker (zero selecting the default) for
// FWSTATE_TEST_WORKER_COUNT workers and one table layer, using the real
// object constructors.
//
// Returns NULL on failure.
struct cp_object *
fwstate_test_map_object_new(
	struct agent *agent, bool is_ipv6, const char *name, uint64_t stash_size
);

// Upsert a map object into the harness agent's current
// configuration-generation object registry — the same registry the
// harness resolves the module's declared link names against. Returns 0
// on success, -1 on failure.
int
fwstate_test_register_object(struct agent *agent, struct cp_object *cp_object);

// Resolve the module config's linked fwtable for one family by reading
// the module's link declaration and looking it up in the agent's object
// registry. Returns NULL when the family is unlinked or the declaration
// matches no registered object.
fwtable_t *
fwstate_test_linked_table(struct cp_module *cp_module, bool is_ipv6);

// Append one layer to both fwtables linked by the module config, mirroring
// the removed in-module layer-growth entry point. Returns 0 or -1.
int
fwstate_test_insert_new_layer(struct cp_module *cp_module);

// Trim expired tail layers from both linked fwtables. Layers reclaimed by
// a previous trim are freed immediately since the harness has no publish
// step to defer them past. Returns 0 or -1.
int
fwstate_test_trim_stale_layers(struct cp_module *cp_module, uint64_t now);

// Resolve the fwmap of a linked table's layer chain: layer 0 is the active
// head. Returns NULL when the family is unlinked or layer_index is past
// the chain end.
fwmap_t *
fwstate_test_table_layer(
	struct cp_module *cp_module, bool is_ipv6, uint32_t layer_index
);

// Accounting snapshot of one memory context, for tests that assert where
// allocations and frees were charged.
struct fwstate_test_mem_counters {
	uint64_t balloc_count;
	uint64_t bfree_count;
	uint64_t balloc_size;
	uint64_t bfree_size;
};

// Snapshot the stand-in agent's context counters into out.
void
fwstate_test_agent_mem_counters(
	struct agent *agent, struct fwstate_test_mem_counters *out
);

// Snapshot a map object's own context counters into out.
void
fwstate_test_object_mem_counters(
	struct cp_object *cp_object, struct fwstate_test_mem_counters *out
);

// Mock implementation of clock_get_time_ns for tests. Returns current
// monotonic time in nanoseconds. Declared here so cgo can call it directly
// from Go.
uint64_t
clock_get_time_ns(struct tsc_clock *clock);
