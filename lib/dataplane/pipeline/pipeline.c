#include "pipeline.h"

#include "common/numutils.h"

#include "lib/counters/histogram.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/time/tsc.h"

#include <rte_cycles.h>

static inline void
counter_add_packets_bytes(
	struct counter_value_handle *counter, uint64_t packets, uint64_t bytes
) {
	if (packets == 0) {
		return;
	}

	uint64_t *values = counter_handle_get_value(counter);
	values[0] += packets;
	values[1] += bytes;
}

static inline void
module_ectx_process(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front

) {
	uint64_t pending_input_count =
		packet_front_pending_input_count(packet_front);
	uint64_t pending_input_bytes =
		packet_front_pending_input_bytes(packet_front);

	uint64_t pending_output_count =
		packet_front_pending_output_count(packet_front);
	uint64_t pending_output_bytes =
		packet_front_pending_output_bytes(packet_front);

	uint64_t drop_count = packet_front_drop_count(packet_front);
	uint64_t drop_bytes = packet_front_drop_bytes(packet_front);

	const size_t packets_count = packet_front_input_count(packet_front);

	for (struct packet *packet = packet_front->input.first; packet != NULL;
	     packet = packet->next) {
		packet->module_device_id = module_ectx_decode_device(
			module_ectx, packet->tx_device_id
		);
	}

	const uint64_t input_bytes = packet_front_input_bytes(packet_front);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&module_ectx->rx_counter),
		packets_count,
		input_bytes
	);

	uint64_t tsc_start = rte_rdtsc();
	module_ectx->handler(dp_worker, module_ectx, packet_front);
	uint64_t tsc_end = rte_rdtsc();

	// update counter for corresponding batch
	uint64_t elapsed_ns = tsc_elapsed_ns(tsc_end - tsc_start);
	if (packets_count > 0) {
		size_t idx = uint64_log_up(packets_count);
		size_t batch_idx = idx < MODULE_ECTX_PERF_COUNTERS
					   ? idx
					   : MODULE_ECTX_PERF_COUNTERS - 1;
		size_t hist_idx = counters_hybrid_histogram_batch(
			&module_ectx_perf_counter, elapsed_ns / packets_count
		);
		struct module_ectx_perf_counter_layout *counter =
			(struct module_ectx_perf_counter_layout *)
				counter_handle_get_value(ADDR_OF_NONNULL(
					&module_ectx->perf_counters[batch_idx]
				));
		counter->summary_latency += elapsed_ns;
		counter->packets += packets_count;
		counter->bytes += input_bytes;
		counter->batch_count[hist_idx] += 1;
	}

	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&module_ectx->tx_counter),
		packet_front_output_count(packet_front),
		packet_front_output_bytes(packet_front)
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&module_ectx->drop_counter),
		packet_front_drop_count(packet_front) - drop_count,
		packet_front_drop_bytes(packet_front) - drop_bytes
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&module_ectx->pending_input_counter),
		packet_front_pending_input_count(packet_front) -
			pending_input_count,
		packet_front_pending_input_bytes(packet_front) -
			pending_input_bytes
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&module_ectx->pending_output_counter),
		packet_front_pending_output_count(packet_front) -
			pending_output_count,
		packet_front_pending_output_bytes(packet_front) -
			pending_output_bytes
	);
}

struct dp_config *
module_ectx_dp_config(struct module_ectx *module_ectx) {
	struct config_gen_ectx *config_gen_ectx =
		ADDR_OF(&module_ectx->config_gen_ectx);
	if (config_gen_ectx == NULL) {
		return NULL;
	}

	struct cp_config_gen *cp_config_gen =
		ADDR_OF(&config_gen_ectx->cp_config_gen);
	if (cp_config_gen == NULL) {
		return NULL;
	}

	return ADDR_OF(&cp_config_gen->dp_config);
}

static inline void
chain_ectx_process(
	struct dp_worker *dp_worker,
	struct chain_ectx *chain_ectx,
	struct packet_front *packet_front
) {
	// A chain with no modules is a wire: pass input through to output so
	// the caller's merge carries the packets onward.
	if (chain_ectx->length == 0) {
		packet_front_pass(packet_front);
		return;
	}

	uint64_t input_size = packet_front_input_count(packet_front);

	uint64_t tsc_start = rte_rdtsc();

	for (uint64_t idx = 0; idx < chain_ectx->length; ++idx) {
		if (idx > 0) {
			packet_front_switch(packet_front);
		}

		struct module_ectx *module_ectx =
			ADDR_OF(&chain_ectx->modules[idx].module_ectx);

		module_ectx_process(dp_worker, module_ectx, packet_front);

		uint64_t tsc_stop = rte_rdtsc();
		counter_hist_exp2_inc(
			ADDR_OF_NONNULL(&chain_ectx->modules[idx].tsc_counter),
			0,
			7,
			input_size,
			tsc_stop - tsc_start
		);
		tsc_start = tsc_stop;
	}

	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&chain_ectx->counter_packet_pending_input),
		packet_front_pending_input_count(packet_front),
		packet_front_pending_input_bytes(packet_front)
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&chain_ectx->counter_packet_pending_output),
		packet_front_pending_output_count(packet_front),
		packet_front_pending_output_bytes(packet_front)
	);
}

// Run the function's only chain.
//
// With a single chain every packet maps to chain 0, so the per-packet hash
// demux is skipped: the whole list is moved to a private front in one step.
// The chain still runs on its own front, so drops accumulated by earlier
// functions stay hidden from this chain's modules, matching the isolation the
// per-chain demux provided.
static void
function_ectx_run_single_chain(
	struct dp_worker *dp_worker,
	struct function_ectx *function_ectx,
	struct packet_front *packet_front
) {
	struct chain_ectx **chains = ADDR_OF(&function_ectx->chains);
	struct chain_ectx *chain_ectx = ADDR_OF(chains);

	struct packet_front *schedule = &chain_ectx->schedule;
	packet_front_take_input(schedule, packet_front);

	chain_ectx_process(dp_worker, chain_ectx, schedule);

	packet_front_merge(packet_front, schedule);
}

// Demultiplex packets across the function's chains by hash.
//
// Each chain is processed on its own packet front and the results are merged
// back into the caller's front.
static void
function_ectx_run_chains(
	struct dp_worker *dp_worker,
	struct function_ectx *function_ectx,
	struct packet_front *packet_front
) {
	struct packet *packet = packet_list_pop(&packet_front->output);
	while (packet != NULL) {
		uint64_t map_idx = packet->hash % function_ectx->chain_map_size;

		struct chain_ectx *chain_ectx =
			ADDR_OF(function_ectx->chain_map + map_idx);
		packet_front_input(&chain_ectx->schedule, packet);

		packet = packet_list_pop(&packet_front->output);
	}
	packet_front->output_count = 0;
	packet_front->output_bytes = 0;

	struct chain_ectx **chains = ADDR_OF(&function_ectx->chains);

	for (uint64_t idx = 0; idx < function_ectx->chain_count; ++idx) {
		struct chain_ectx *chain_ectx = ADDR_OF(chains + idx);

		chain_ectx_process(
			dp_worker, chain_ectx, &chain_ectx->schedule
		);

		packet_front_merge(packet_front, &chain_ectx->schedule);
	}
}

// Drain a function whose chains are all zero-weight (fully disabled).
//
// There is no chain to route packets to, so the output is dropped. The chains
// are still scheduled on now-empty fronts so the worker keeps force-polling
// every module once per tick for periodic work, exactly as the demux path did
// for a function with no packets to route.
static void
function_ectx_drain(
	struct dp_worker *dp_worker,
	struct function_ectx *function_ectx,
	struct packet_front *packet_front
) {
	packet_front_drop_output(packet_front);

	function_ectx_run_chains(dp_worker, function_ectx, packet_front);
}

static inline void
function_ectx_process(
	struct dp_worker *dp_worker,
	struct function_ectx *function_ectx,
	struct packet_front *packet_front
) {
	uint64_t drop_count = packet_front_drop_count(packet_front);
	uint64_t drop_bytes = packet_front_drop_bytes(packet_front);

	uint64_t pending_input_count =
		packet_front_pending_input_count(packet_front);
	uint64_t pending_input_bytes =
		packet_front_pending_input_bytes(packet_front);

	uint64_t pending_output_count =
		packet_front_pending_output_count(packet_front);
	uint64_t pending_output_bytes =
		packet_front_pending_output_bytes(packet_front);

	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&function_ectx->counter_packet_in),
		packet_front_output_count(packet_front),
		packet_front_output_bytes(packet_front)
	);

	if (function_ectx->chain_map_size == 0) {
		function_ectx_drain(dp_worker, function_ectx, packet_front);
	} else if (function_ectx->chain_count == 1) {
		function_ectx_run_single_chain(
			dp_worker, function_ectx, packet_front
		);
	} else {
		function_ectx_run_chains(
			dp_worker, function_ectx, packet_front
		);
	}

	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&function_ectx->counter_packet_out),
		packet_front_output_count(packet_front),
		packet_front_output_bytes(packet_front)
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&function_ectx->counter_packet_drop),
		packet_front_drop_count(packet_front) - drop_count,
		packet_front_drop_bytes(packet_front) - drop_bytes
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&function_ectx->counter_packet_pending_input),
		packet_front_pending_input_count(packet_front) -
			pending_input_count,
		packet_front_pending_input_bytes(packet_front) -
			pending_input_bytes
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&function_ectx->counter_packet_pending_output),
		packet_front_pending_output_count(packet_front) -
			pending_output_count,
		packet_front_pending_output_bytes(packet_front) -
			pending_output_bytes
	);
}

static inline void
pipeline_ectx_process(
	struct dp_worker *dp_worker,
	struct pipeline_ectx *pipeline_ectx,
	struct packet_front *packet_front
) {
	// Packets arrive in output list, count them before processing
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&pipeline_ectx->counter_packet_in),
		packet_front_output_count(packet_front),
		packet_front_output_bytes(packet_front)
	);

	for (uint64_t idx = 0; idx < pipeline_ectx->length; ++idx) {
		struct function_ectx *function_ectx =
			ADDR_OF(pipeline_ectx->functions + idx);

		function_ectx_process(dp_worker, function_ectx, packet_front);
	}

	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&pipeline_ectx->counter_packet_out),
		packet_front_output_count(packet_front),
		packet_front_output_bytes(packet_front)
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&pipeline_ectx->counter_packet_drop),
		packet_front_drop_count(packet_front),
		packet_front_drop_bytes(packet_front)
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&pipeline_ectx->counter_packet_pending_input),
		packet_front_pending_input_count(packet_front),
		packet_front_pending_input_bytes(packet_front)
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&pipeline_ectx->counter_packet_pending_output),
		packet_front_pending_output_count(packet_front),
		packet_front_pending_output_bytes(packet_front)
	);
}

// Run the entry's only pipeline.
//
// With a single pipeline every packet maps to pipeline 0, so the per-packet
// hash demux is skipped: the whole list is moved to a private front in one
// step. The pipeline still runs on its own front, so drops accumulated by the
// device handler stay hidden from its functions, matching the isolation the
// per-pipeline demux provided.
static inline void
device_entry_ectx_dispatch_single(
	struct dp_worker *dp_worker,
	struct device_entry_ectx *entry_ectx,
	struct packet_front *packet_front
) {
	struct pipeline_ectx **pipelines = ADDR_OF(&entry_ectx->pipelines);
	struct pipeline_ectx *pipeline_ectx = ADDR_OF(pipelines);

	struct packet_front *schedule = &pipeline_ectx->schedule;
	packet_front_take_output(schedule, packet_front);

	pipeline_ectx_process(dp_worker, pipeline_ectx, schedule);

	packet_front_merge(packet_front, schedule);
}

// Demultiplex the handler output across the entry's pipelines by hash.
//
// Each pipeline is processed on its own packet front and the results are merged
// back into the caller's front.
static inline void
device_entry_ectx_dispatch_many(
	struct dp_worker *dp_worker,
	struct device_entry_ectx *entry_ectx,
	struct packet_front *packet_front
) {
	struct pipeline_ectx **pipelines = ADDR_OF(&entry_ectx->pipelines);

	struct packet *packet = packet_list_pop(&packet_front->output);
	while (packet != NULL) {
		uint64_t pipeline_idx =
			entry_ectx->pipeline_map
				[packet->hash % entry_ectx->pipeline_map_size];

		struct pipeline_ectx *pipeline_ectx =
			ADDR_OF(pipelines + pipeline_idx);
		packet_front_output(&pipeline_ectx->schedule, packet);

		packet = packet_list_pop(&packet_front->output);
	}
	packet_front->output_count = 0;
	packet_front->output_bytes = 0;

	for (uint64_t idx = 0; idx < entry_ectx->pipeline_count; ++idx) {
		struct pipeline_ectx *pipeline_ectx = ADDR_OF(pipelines + idx);

		pipeline_ectx_process(
			dp_worker, pipeline_ectx, &pipeline_ectx->schedule
		);

		packet_front_merge(packet_front, &pipeline_ectx->schedule);
	}
}

// Drain a device entry that has no routable pipeline.
//
// The unroutable output is dropped. If the entry has pipelines but they are all
// zero-weight, they are still scheduled on now-empty fronts so the worker keeps
// force-polling every module once per tick for periodic work; reusing the demux
// is safe once the output list is empty, since its per-packet loop never runs
// and the modulo by the zero pipeline-map size is never reached. An entry with
// no pipelines at all has nothing to poll, so the demux — which would size a
// zero-length scheduling array — is skipped.
static inline void
device_entry_ectx_drain(
	struct dp_worker *dp_worker,
	struct device_entry_ectx *entry_ectx,
	struct packet_front *packet_front
) {
	packet_front_drop_output(packet_front);

	if (entry_ectx->pipeline_count > 0) {
		device_entry_ectx_dispatch_many(
			dp_worker, entry_ectx, packet_front
		);
	}
}

// Demultiplex the handler output across the entry's pipelines.
static inline void
device_entry_ectx_dispatch(
	struct dp_worker *dp_worker,
	struct device_entry_ectx *entry_ectx,
	struct packet_front *packet_front
) {
	if (entry_ectx->pipeline_map_size == 0) {
		device_entry_ectx_drain(dp_worker, entry_ectx, packet_front);
	} else if (entry_ectx->pipeline_count == 1) {
		device_entry_ectx_dispatch_single(
			dp_worker, entry_ectx, packet_front
		);
	} else {
		device_entry_ectx_dispatch_many(
			dp_worker, entry_ectx, packet_front
		);
	}
}

static inline void
device_ectx_process_entry(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct device_entry_ectx *entry_ectx,
	struct packet_front *packet_front
) {
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&entry_ectx->counter_packet_rx),
		packet_front_input_count(packet_front),
		packet_front_input_bytes(packet_front)
	);

	entry_ectx->handler(dp_worker, device_ectx, packet_front);

	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&entry_ectx->counter_packet_entry),
		packet_front_output_count(packet_front),
		packet_front_output_bytes(packet_front)
	);

	device_entry_ectx_dispatch(dp_worker, entry_ectx, packet_front);

	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&entry_ectx->counter_packet_tx),
		packet_front_output_count(packet_front),
		packet_front_output_bytes(packet_front)
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&entry_ectx->counter_packet_drop),
		packet_front_drop_count(packet_front),
		packet_front_drop_bytes(packet_front)
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&entry_ectx->counter_packet_pending_input),
		packet_front_pending_input_count(packet_front),
		packet_front_pending_input_bytes(packet_front)
	);
	counter_add_packets_bytes(
		ADDR_OF_NONNULL(&entry_ectx->counter_packet_pending_output),
		packet_front_pending_output_count(packet_front),
		packet_front_pending_output_bytes(packet_front)
	);
}

void
device_ectx_process_input(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	struct device_entry_ectx *entry_ectx =
		ADDR_OF(&device_ectx->input_pipelines);

	device_ectx_process_entry(
		dp_worker, device_ectx, entry_ectx, packet_front
	);
}

void
device_ectx_process_output(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	struct device_entry_ectx *entry_ectx =
		ADDR_OF(&device_ectx->output_pipelines);

	device_ectx_process_entry(
		dp_worker, device_ectx, entry_ectx, packet_front
	);
}
