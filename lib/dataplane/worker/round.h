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
// release publication and iteration increment retain production ordering.
static inline struct worker_round
worker_round_prepare(
	struct dp_worker *dp_worker,
	struct cp_config *cp_config,
	uint64_t current_time_ns
) {
	__atomic_store_n(
		&dp_worker->current_time, current_time_ns, __ATOMIC_RELAXED
	);

	struct worker_round round = {
		.cp_config_gen = ATOMIC_ADDR_OF(&cp_config->cp_config_gen),
	};
	round.config_gen_ectx =
		cp_config_gen_worker_ectx(round.cp_config_gen, dp_worker->idx);

	__atomic_store_n(
		&dp_worker->gen, round.cp_config_gen->gen, __ATOMIC_RELEASE
	);
	*dp_worker->iterations += 1;

	// Flip or first-build the worklists before any packet of this tick
	// is scheduled onto them.
	if (round.config_gen_ectx != NULL) {
		config_gen_ectx_schedules_prepare(round.config_gen_ectx);
	}

	return round;
}
