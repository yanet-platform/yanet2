#pragma once

#include <stdint.h>

struct dp_worker;
struct cp_config_gen;
struct config_gen_ectx;
struct device_entry_ectx;
struct packet_front;
struct rlist;

// Decide the egress of the packets that finished an output entry.
//
// The entry's device resolves through the backing table once, and every
// finisher lands in that port's transmit bucket. A device with no port
// has its finishers join the front's drop list, counted against the
// entry's drop counter, instead of travelling namelessly to the write
// stage.
void
worker_entry_terminal_egress(
	struct config_gen_ectx *config_gen_ectx,
	struct device_entry_ectx *entry_ectx,
	struct packet_front *packet_front
);

// Move the entries whose deadline has arrived onto the ready list.
//
// The sweep of one round: the caller has already drained the generation's
// home list onto the untouched list, and current_time_ns is the round's
// time. Entries due at or before the time move onto the ready list; the
// cached earliest deadline is recomputed from the deadlines that survive
// on the untouched list. Exposed separately from the round so the
// dataplane_ut harness can drive the sweep without the rest of the round.
void
worker_pipeline_round_arm_due(
	struct config_gen_ectx *config_gen_ectx,
	struct rlist *untouched,
	uint64_t current_time_ns
);

// Drain the device entry schedules through the pipeline.
//
// The caller has already snapshotted cp_config_gen and ensured a non-NULL
// config_gen_ectx, set dp_worker->current_time, incremented the worker
// iteration counter, and placed RX packets (and any module-routed
// recirculation) directly onto the target device entry schedules.
// config_gen_ectx must be non-NULL.
//
// The round runs on the generation context's scratch front: on return its
// output holds packets that should be written out (or onward), its drop
// list holds every packet the pipeline rejected — the finished chain drop
// lists are spliced into it directly during processing — and every device
// entry schedule is left empty. The caller owns draining the front before
// the next round.
void
worker_pipeline_round(
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct config_gen_ectx *config_gen_ectx
);
