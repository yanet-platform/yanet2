#pragma once

struct dp_worker;
struct cp_config_gen;
struct config_gen_ectx;
struct packet_front;

// Drain pending_input and pending_output through the device pipeline.
//
// The caller has already snapshotted cp_config_gen and ensured a non-NULL
// config_gen_ectx, set dp_worker->current_time, incremented the worker
// iteration counter, and populated packet_front->pending_input or
// pending_output with packets ready for pipeline processing.
// config_gen_ectx must be non-NULL.
//
// On return, packet_front->output holds packets that should be written
// out (or onward), packet_front->drop holds packets the pipeline rejected,
// and both pending_input and pending_output are empty.
void
worker_pipeline_round(
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct config_gen_ectx *config_gen_ectx,
	struct packet_front *packet_front
);
