#pragma once

struct dp_worker;
struct cp_config_gen;
struct config_gen_ectx;

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
