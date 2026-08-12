#pragma once

#include <stdlib.h> // IWYU pragma: export

#include "common/memory.h" // IWYU pragma: export
#include "lib/controlplane/agent/agent.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/time/clock.h"
#include "lib/errors/errors.h"	// IWYU pragma: export
#include "lib/fwstate/config.h" // IWYU pragma: export
#include "lib/fwstate/fwmap.h"	// IWYU pragma: export
#include "lib/fwstate/layermap.h"
#include "lib/fwstate/types.h" // IWYU pragma: export
#include "modules/fwstate/api/fwstate_cp.h"
#include "modules/fwstate/dataplane/config.h" // IWYU pragma: export

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

// Test wrapper for fwstate_handle_packets that constructs module_ectx from
// cp_module and a pre-spawned counter storage. The storage is owned by the
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

// Mock implementation of clock_get_time_ns for tests. Returns current
// monotonic time in nanoseconds. Declared here so cgo can call it directly
// from Go.
uint64_t
clock_get_time_ns(struct tsc_clock *clock);