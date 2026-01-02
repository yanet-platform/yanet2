#include "pipeline.h"

#include "counters/utils.h"

#include "controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/packet/packet_list.h"

#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/dataplane/worker/worker.h"

#include "lib/logging/log.h"

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
	uint64_t counter_id,
	uint64_t worker_idx,
	struct counter_storage *storage,
	uint64_t packets,
	uint64_t bytes
) {
	uint64_t *values = counter_get_address(counter_id, worker_idx, storage);
	values[0] += packets;
	values[1] += bytes;
}

void
module_ectx_process(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front

) {

	for (struct packet *packet = packet_front->input.first; packet != NULL;
	     packet = packet->next) {
		packet->module_device_id = module_ectx_decode_device(
			module_ectx, packet->device_id
		);
	}

	uint64_t *rx_counters = counter_handle_get_value(
		ADDR_OF(&module_ectx->rx_counter), dp_worker->idx
	);
	rx_counters[0] += packet_front->input.count;
	rx_counters[1] += packet_list_bytes_sum(&packet_front->input);

	module_ectx->handler(dp_worker, module_ectx, packet_front);

	uint64_t *tx_counters = counter_handle_get_value(
		ADDR_OF(&module_ectx->tx_counter), dp_worker->idx
	);
	tx_counters[0] += packet_front->output.count;
	tx_counters[1] += packet_list_bytes_sum(&packet_front->output);

	LOG_TRACEX(int in = packet_list_counter(&packet_front->input);
		   int out = packet_list_counter(&packet_front->output);
		   int drop = packet_list_counter(&packet_front->drop);
		   struct cp_module *cp_module =
			   ADDR_OF(&module_ectx->cp_module);

		   packet_list_print(&packet_front->output);
		   ,
		   "processed packets with module %s, in %d, out "
		   "%d, drop %d. Output list printed above.",
		   cp_module->name,
		   in,
		   out,
		   drop);
}

void
chain_ectx_process(
	struct dp_worker *dp_worker,
	struct chain_ectx *chain_ectx,
	struct packet_front *packet_front
) {
	uint64_t input_size = packet_list_count(&packet_front->output);

	uint64_t tsc_start = rte_rdtsc();

	for (uint64_t idx = 0; idx < chain_ectx->length; ++idx) {
		packet_front_switch(packet_front);

		struct module_ectx *module_ectx =
			ADDR_OF(&chain_ectx->modules[idx].module_ectx);

		module_ectx_process(dp_worker, module_ectx, packet_front);

		uint64_t tsc_stop = rte_rdtsc();

		uint64_t *counters = counter_handle_get_value(
			ADDR_OF(&chain_ectx->modules[idx].tsc_counter),
			dp_worker->idx
		);

		counters[input_size] += tsc_stop - tsc_start;

		tsc_start = tsc_stop;
	}
}

void
function_ectx_process(
	struct dp_worker *dp_worker,
	struct function_ectx *function_ectx,
	struct packet_front *packet_front
) {
	uint64_t *rx_counters = counter_handle_get_value(
		ADDR_OF(&function_ectx->rx_counter), dp_worker->idx
	);

	rx_counters[0] += packet_list_count(&packet_front->output);
	rx_counters[1] += packet_list_bytes_sum(&packet_front->output);

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

	uint64_t *tx_counters = counter_handle_get_value(
		ADDR_OF(&function_ectx->tx_counter), dp_worker->idx
	);

	tx_counters[0] += packet_list_count(&packet_front->output);
	tx_counters[1] += packet_list_bytes_sum(&packet_front->output);

	uint64_t *drop_counters = counter_handle_get_value(
		ADDR_OF(&function_ectx->drop_counter), dp_worker->idx
	);

	drop_counters[0] += packet_list_count(&packet_front->drop);
	drop_counters[1] += packet_list_bytes_sum(&packet_front->drop);
}

void
pipeline_ectx_process(
	struct dp_worker *dp_worker,
	struct pipeline_ectx *pipeline_ectx,
	struct packet_front *packet_front
) {
	counter_handle_get_value(
		ADDR_OF(&pipeline_ectx->counter_batch_size), dp_worker->idx
	)[packet_list_count(&packet_front->output)] += 1;

	// Packets arrive in output list, count them before processing
	uint64_t *rx_counters = counter_handle_get_value(
		ADDR_OF(&pipeline_ectx->rx_counter), dp_worker->idx
	);
	rx_counters[0] += packet_list_count(&packet_front->output);
	rx_counters[1] += packet_list_bytes_sum(&packet_front->output);

	for (uint64_t idx = 0; idx < pipeline_ectx->length; ++idx) {
		struct function_ectx *function_ectx =
			ADDR_OF(pipeline_ectx->functions + idx);

		function_ectx_process(dp_worker, function_ectx, packet_front);
	}

	uint64_t *tx_counters = counter_handle_get_value(
		ADDR_OF(&pipeline_ectx->tx_counter), dp_worker->idx
	);
	tx_counters[0] += packet_list_count(&packet_front->output);
	tx_counters[1] += packet_list_bytes_sum(&packet_front->output);

	uint64_t *drop_counters = counter_handle_get_value(
		ADDR_OF(&pipeline_ectx->drop_counter), dp_worker->idx
	);
	drop_counters[0] += packet_list_count(&packet_front->drop);
	drop_counters[1] += packet_list_bytes_sum(&packet_front->drop);
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
		uint32_t pipeline_idx =
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
	uint64_t *counters = counter_handle_get_value(
		ADDR_OF(&device_ectx->counter_rx), dp_worker->idx
	);
	counters[0] += packet_list_count(&packet_front->output);
	counters[1] += packet_list_bytes_sum(&packet_front->output);

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
	uint64_t *counters = counter_handle_get_value(
		ADDR_OF(&device_ectx->counter_tx), dp_worker->idx
	);
	counters[0] += packet_list_count(&packet_front->output);
	counters[1] += packet_list_bytes_sum(&packet_front->output);

	struct device_entry_ectx *entry_ectx =
		ADDR_OF(&device_ectx->output_pipelines);
	device_entry_ectx_process(
		dp_worker, device_ectx, entry_ectx, packet_front
	);
}
