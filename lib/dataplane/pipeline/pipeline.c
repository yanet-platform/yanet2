#include "pipeline.h"

#include "common/numutils.h"

#include "counters/histogram.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "dataplane/config/zone.h"
#include "dataplane/packet/packet.h"
#include "dataplane/time/tsc.h"
#include "lib/dataplane/module/packet_front.h"

#include <rte_cycles.h>

static inline void
counter_add(
	uint64_t counter_id,
	uint64_t worker_idx,
	struct counter_storage *storage,
	uint64_t count
) {
	counter_get_address(counter_id, worker_idx, storage)[0] += count;
}

static inline void
counter_add_packets_bytes(
	uint64_t packets_id,
	uint64_t bytes_id,
	uint64_t worker_idx,
	struct counter_storage *storage,
	uint64_t packets,
	uint64_t bytes
) {
	counter_add(packets_id, worker_idx, storage, packets);
	counter_add(bytes_id, worker_idx, storage, bytes);
}

void
module_ectx_process(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front

) {
	const size_t packets_count = packet_front->input.count;

	for (struct packet *packet = packet_front->input.first; packet != NULL;
	     packet = packet->next) {
		packet->module_device_id = module_ectx_decode_device(
			module_ectx, packet->tx_device_id
		);
	}

	struct counter_storage *storage =
		ADDR_OF(&module_ectx->counter_storage);

	const uint64_t input_bytes =
		packet_list_bytes_sum(&packet_front->input);
	counter_add_packets_bytes(
		module_ectx->rx_counter_id,
		module_ectx->rx_bytes_counter_id,
		dp_worker->idx,
		storage,
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
		size_t counter_idx =
			module_ectx->perf_counters_indices[batch_idx];
		size_t hist_idx = counters_hybrid_histogram_batch(
			&module_ectx_perf_counter, elapsed_ns / packets_count
		);
		struct module_ectx_perf_counter_layout *counter =
			(struct module_ectx_perf_counter_layout *)
				counter_get_address(
					counter_idx, dp_worker->idx, storage
				);
		counter->summary_latency += elapsed_ns;
		counter->packets += packets_count;
		counter->bytes += input_bytes;
		counter->batch_count[hist_idx] += 1;
	}

	counter_add_packets_bytes(
		module_ectx->tx_counter_id,
		module_ectx->tx_bytes_counter_id,
		dp_worker->idx,
		storage,
		packet_front->output.count,
		packet_list_bytes_sum(&packet_front->output)
	);
}

void
chain_ectx_process(
	struct dp_worker *dp_worker,
	struct chain_ectx *chain_ectx,
	struct packet_front *packet_front
) {
	uint64_t input_size = packet_list_count(&packet_front->input);

	uint64_t tsc_start = rte_rdtsc();

	for (uint64_t idx = 0; idx < chain_ectx->length; ++idx) {
		packet_front_switch(packet_front);

		struct module_ectx *module_ectx =
			ADDR_OF(&chain_ectx->modules[idx].module_ectx);

		module_ectx_process(dp_worker, module_ectx, packet_front);

		uint64_t tsc_stop = rte_rdtsc();
		counter_hist_exp2_inc(
			chain_ectx->modules[idx].tsc_counter_id,
			dp_worker->idx,
			ADDR_OF(&chain_ectx->counter_storage),
			0,
			7,
			input_size,
			tsc_stop - tsc_start
		);
		tsc_start = tsc_stop;
	}
}

void
function_ectx_process(
	struct dp_worker *dp_worker,
	struct function_ectx *function_ectx,
	struct packet_front *packet_front
) {
	struct counter_storage *storage =
		ADDR_OF(&function_ectx->counter_storage);

	counter_add_packets_bytes(
		function_ectx->counter_packet_in_count,
		function_ectx->counter_packet_in_bytes,
		dp_worker->idx,
		storage,
		packet_front->output.count,
		packet_list_bytes_sum(&packet_front->output)
	);

	// FIXME: do not create schedule for each invocation
	struct packet_front schedule[function_ectx->chain_count];
	for (uint64_t idx = 0; idx < function_ectx->chain_count; ++idx)
		packet_front_init(schedule + idx);

	struct packet *packet = packet_list_pop(&packet_front->output);
	while (packet != NULL) {
		uint64_t chain_idx =
			function_ectx->chain_map
				[packet->hash % function_ectx->chain_map_size];
		packet_front_output(schedule + chain_idx, packet);
		packet = packet_list_pop(&packet_front->output);
	}

	struct chain_ectx **chains = ADDR_OF(&function_ectx->chains);
	for (uint64_t idx = 0; idx < function_ectx->chain_count; ++idx) {
		struct chain_ectx *chain_ectx = ADDR_OF(chains + idx);

		chain_ectx_process(dp_worker, chain_ectx, schedule + idx);

		packet_front_merge(packet_front, schedule + idx);
	}

	counter_add_packets_bytes(
		function_ectx->counter_packet_out_count,
		function_ectx->counter_packet_out_bytes,
		dp_worker->idx,
		storage,
		packet_front->output.count,
		packet_list_bytes_sum(&packet_front->output)
	);
	counter_add_packets_bytes(
		function_ectx->counter_packet_drop_count,
		function_ectx->counter_packet_drop_bytes,
		dp_worker->idx,
		storage,
		packet_front->drop.count,
		packet_list_bytes_sum(&packet_front->drop)
	);
}

void
pipeline_ectx_process(
	struct dp_worker *dp_worker,
	struct pipeline_ectx *pipeline_ectx,
	struct packet_front *packet_front
) {
	struct counter_storage *storage =
		ADDR_OF(&pipeline_ectx->counter_storage);

	// Packets arrive in output list, count them before processing
	counter_add_packets_bytes(
		pipeline_ectx->counter_packet_in_count,
		pipeline_ectx->counter_packet_in_bytes,
		dp_worker->idx,
		storage,
		packet_front->output.count,
		packet_list_bytes_sum(&packet_front->output)
	);

	for (uint64_t idx = 0; idx < pipeline_ectx->length; ++idx) {
		struct function_ectx *function_ectx =
			ADDR_OF(pipeline_ectx->functions + idx);

		function_ectx_process(dp_worker, function_ectx, packet_front);
	}

	counter_add_packets_bytes(
		pipeline_ectx->counter_packet_out_count,
		pipeline_ectx->counter_packet_out_bytes,
		dp_worker->idx,
		storage,
		packet_front->output.count,
		packet_list_bytes_sum(&packet_front->output)
	);
	counter_add_packets_bytes(
		pipeline_ectx->counter_packet_drop_count,
		pipeline_ectx->counter_packet_drop_bytes,
		dp_worker->idx,
		storage,
		packet_front->drop.count,
		packet_list_bytes_sum(&packet_front->drop)
	);
}

static inline void
device_entry_ectx_process(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct device_entry_ectx *entry_ectx,
	struct packet_front *packet_front
) {
	packet_front_switch(packet_front);
	entry_ectx->handler(dp_worker, device_ectx, packet_front);

	if (!entry_ectx->pipeline_map_size) {
		packet_list_concat(&packet_front->drop, &packet_front->output);
		packet_list_init(&packet_front->output);
		return;
	}

	// FIXME do not create front for each invocation
	struct packet_front schedule[entry_ectx->pipeline_count];
	for (uint64_t idx = 0; idx < entry_ectx->pipeline_count; ++idx) {
		packet_front_init(schedule + idx);
	}

	struct packet *packet = packet_list_pop(&packet_front->output);
	while (packet != NULL) {
		uint64_t pipeline_idx =
			entry_ectx->pipeline_map
				[packet->hash % entry_ectx->pipeline_map_size];

		packet_front_output(schedule + pipeline_idx, packet);

		packet = packet_list_pop(&packet_front->output);
	}

	struct pipeline_ectx **pipelines = ADDR_OF(&entry_ectx->pipelines);
	for (uint64_t idx = 0; idx < entry_ectx->pipeline_count; ++idx) {
		struct pipeline_ectx *pipeline_ectx = ADDR_OF(pipelines + idx);

		pipeline_ectx_process(dp_worker, pipeline_ectx, schedule + idx);

		packet_front_merge(packet_front, schedule + idx);
	}
}

void
device_ectx_process_input(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	counter_add_packets_bytes(
		device_ectx->counter_packet_rx_count,
		device_ectx->counter_packet_rx_bytes,
		dp_worker->idx,
		ADDR_OF(&device_ectx->counter_storage),
		packet_list_count(&packet_front->output),
		packet_list_bytes_sum(&packet_front->output)
	);

	struct device_entry_ectx *entry_ectx =
		ADDR_OF(&device_ectx->input_pipelines);
	device_entry_ectx_process(
		dp_worker, device_ectx, entry_ectx, packet_front
	);
}

void
device_ectx_process_output(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
) {
	counter_add_packets_bytes(
		device_ectx->counter_packet_tx_count,
		device_ectx->counter_packet_tx_bytes,
		dp_worker->idx,
		ADDR_OF(&device_ectx->counter_storage),
		packet_list_count(&packet_front->output),
		packet_list_bytes_sum(&packet_front->output)
	);

	struct device_entry_ectx *entry_ectx =
		ADDR_OF(&device_ectx->output_pipelines);
	device_entry_ectx_process(
		dp_worker, device_ectx, entry_ectx, packet_front
	);
}
