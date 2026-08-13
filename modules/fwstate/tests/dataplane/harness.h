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
#include "lib/fwstate/fwmap.h"	// IWYU pragma: export
#include "lib/fwstate/layermap.h"
#include "lib/fwstate/types.h" // IWYU pragma: export
#include "modules/fwstate/api/fwstate_cp.h"
#include "modules/fwstate/dataplane/config.h" // IWYU pragma: export
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

// Test wrapper for fwstate_handle_packets that constructs module_ectx
// from cp_module and a pre-spawned counter storage, wiring the module's
// declared object links through the agent's object registry the way the
// production execution-context build does. The storage is owned by the
// caller and reused across calls so counters accumulate.
void
test_fwstate_handle_packets(
	struct dp_worker *dp_worker,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
);

// Helper to get actual pointer from an offset pointer.
void *
addr_of(void **field);

// Create a standalone fwstate-map object of the requested family with one
// table layer, using the real object constructors. The dp_config built by
// fwstate_test_agent_new carries the object types, so cp_object_init
// resolves them exactly as in production.
//
// Returns NULL on failure.
struct cp_object *
fwstate_test_map_object_new(
	struct agent *agent, bool is_ipv6, const char *name
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

// Mock implementation of clock_get_time_ns for tests. Returns current
// monotonic time in nanoseconds. Declared here so cgo can call it directly
// from Go.
uint64_t
clock_get_time_ns(struct tsc_clock *clock);