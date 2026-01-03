#pragma once

#include "common/memory.h"

#include <stddef.h>
#include <stdint.h>

#include "common/rcu.h"

#include "api/vs.h"
#include "state/worker.h"

#include "real.h"

////////////////////////////////////////////////////////////////////////////////

#define SELECTOR_VALUE_INVALID ((uint32_t)-1)

////////////////////////////////////////////////////////////////////////////////

/**
 * Compact ring of backend identifiers for selection.
 */
struct ring {
	uint32_t len; // Number of entries in ids

	// Relative pointer to per-backend identifiers (packet-handler indices)
	uint32_t *ids;
};

/**
 * Per-worker selector state.
 */
struct selector_worker {
	uint64_t rr_counter; // Round-robin position
} __attribute__((aligned(64)));

/**
 * Real backend selector.
 *
 * Maintains two rings for RCU-swapped updates and per-worker RR counters.
 * Uses either round-robin or hash-based selection depending on VS scheduler.
 */
struct real_selector {
	struct memory_context mctx; // Memory context for rings
	rcu_t rcu;		    // RCU guard for ring swaps
	struct selector_worker workers[MAX_WORKERS_NUM]; // Per-worker state
	struct ring rings[2];	// Double-buffered rings
	_Atomic size_t ring_id; // Active ring index
	int use_rr;		// Non-zero for RR, zero for hash
};

/**
 * Initialize selector with desired scheduling mode.
 * Returns 0 on success, -1 on error.
 */
int
selector_init(
	struct real_selector *selector,
	struct memory_context *mctx,
	enum vs_scheduler scheduler
);

/**
 * Free resources held by the selector.
 */
void
selector_free(struct real_selector *selector);

/**
 * Rebuild selector rings from provided real views.
 * Returns 0 on success, -1 on error.
 */
int
selector_update(
	struct real_selector *selector,
	size_t reals_count,
	struct real *reals
);
