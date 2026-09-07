#pragma once

#include "common/memory_address.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"

// State shared by the preparation and processing parts of one worker round.
struct worker_round {
	struct cp_config_gen *cp_config_gen;
	struct config_gen_ectx *config_gen_ectx;
};

// Prepare the worker state that precedes packet processing.
//
// The caller evaluates current_time_ns before entering this helper. The
// release acknowledgement and iteration increment retain production
// ordering.
static inline struct worker_round
worker_round_prepare(struct dp_worker *dp_worker, uint64_t current_time_ns) {
	__atomic_store_n(
		&dp_worker->current_time, current_time_ns, __ATOMIC_RELAXED
	);

	// The round acknowledges the generation of the context it actually
	// snapshotted.
	//
	// The release store orders the previous round's uses of a retired
	// context before the acknowledgement that permits its reclamation;
	// a NULL context acknowledges zero, the pre-configuration state.
	struct config_gen_ectx *config_gen_ectx =
		ATOMIC_ADDR_OF(&dp_worker->config_gen_ectx);
	struct cp_config_gen *cp_config_gen = NULL;
	uint64_t acknowledged_gen = 0;
	if (config_gen_ectx != NULL) {
		cp_config_gen = ADDR_OF(&config_gen_ectx->cp_config_gen);
		acknowledged_gen = cp_config_gen->gen;
	}
	struct worker_round round = {
		.cp_config_gen = cp_config_gen,
		.config_gen_ectx = config_gen_ectx,
	};
	__atomic_store_n(&dp_worker->gen, acknowledged_gen, __ATOMIC_RELEASE);
	*dp_worker->iterations += 1;

	// First-build the worklists before any packet is scheduled onto
	// them; later preparations do nothing, the round drains the
	// worklists itself.
	if (round.config_gen_ectx != NULL) {
		config_gen_ectx_schedules_prepare(round.config_gen_ectx);
	}

	return round;
}
