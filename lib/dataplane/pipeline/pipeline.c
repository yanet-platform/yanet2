#include "pipeline.h"

#include "counters/utils.h"

#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"
#include "dataplane/packet/packet.h"
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
	struct dp_config *dp_config,
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front

) {
	(void)dp_config;
	(void)cp_config_gen;

	for (struct packet *packet = packet_front->input.first; packet != NULL;
	     packet = packet->next) {
		packet->module_device_id = module_ectx_decode_device(
			module_ectx, packet->tx_device_id
		);
	}

	struct counter_storage *storage =
		ADDR_OF(&module_ectx->counter_storage);

	counter_add_packets_bytes(
		module_ectx->rx_counter_id,
		module_ectx->rx_bytes_counter_id,
		dp_worker->idx,
		storage,
		packet_front->input.count,
		packet_list_bytes_sum(&packet_front->input)
	);

	module_ectx->handler(dp_worker, module_ectx, packet_front);

	counter_add_packets_bytes(
		module_ectx->tx_counter_id,
		module_ectx->tx_bytes_counter_id,
		dp_worker->idx,
		storage,
		packet_front->output.count,
		packet_list_bytes_sum(&packet_front->output)
	);

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
	struct dp_config *dp_config,
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct chain_ectx *chain_ectx,
	struct packet_front *packet_front
) {
	uint64_t input_size = packet_list_count(&packet_front->input);

	uint64_t tsc_start = rte_rdtsc();

	for (uint64_t idx = 0; idx < chain_ectx->length; ++idx) {
		packet_front_switch(packet_front);

		struct module_ectx *module_ectx =
			ADDR_OF(&chain_ectx->modules[idx].module_ectx);

		module_ectx_process(
			dp_config,
			dp_worker,
			cp_config_gen,
			module_ectx,
			packet_front
		);

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
	struct dp_config *dp_config,
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct function_ectx *function_ectx,
	struct packet_front *packet_front
) {
	struct cp_function *cp_function = ADDR_OF(&function_ectx->cp_function);
	struct counter_storage *storage =
		ADDR_OF(&function_ectx->counter_storage);

	counter_add_packets_bytes(
		cp_function->counter_packet_in_count,
		cp_function->counter_packet_in_bytes,
		dp_worker->idx,
		storage,
		packet_front->output.count,
		packet_list_bytes_sum(&packet_front->output)
	);

	// FIXME route through chains
	uint64_t chain_idx = 0;
	struct chain_ectx *chain_ectx =
		ADDR_OF(function_ectx->chain_map + chain_idx);
	chain_ectx_process(
		dp_config, dp_worker, cp_config_gen, chain_ectx, packet_front
	);

	counter_add_packets_bytes(
		cp_function->counter_packet_out_count,
		cp_function->counter_packet_out_bytes,
		dp_worker->idx,
		storage,
		packet_front->output.count,
		packet_list_bytes_sum(&packet_front->output)
	);
	counter_add_packets_bytes(
		cp_function->counter_packet_drop_count,
		cp_function->counter_packet_drop_bytes,
		dp_worker->idx,
		storage,
		packet_front->drop.count,
		packet_list_bytes_sum(&packet_front->drop)
	);
}

void
pipeline_ectx_process(
	struct dp_config *dp_config,
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct pipeline_ectx *pipeline_ectx,
	struct packet_front *packet_front
) {
	struct cp_pipeline *cp_pipeline = ADDR_OF(&pipeline_ectx->cp_pipeline);
	struct counter_storage *storage =
		ADDR_OF(&pipeline_ectx->counter_storage);

	// Packets arrive in output list, count them before processing
	counter_add_packets_bytes(
		cp_pipeline->counter_packet_in_count,
		cp_pipeline->counter_packet_in_bytes,
		dp_worker->idx,
		storage,
		packet_front->output.count,
		packet_list_bytes_sum(&packet_front->output)
	);

	for (uint64_t idx = 0; idx < pipeline_ectx->length; ++idx) {
		struct function_ectx *function_ectx =
			ADDR_OF(pipeline_ectx->functions + idx);

		function_ectx_process(
			dp_config,
			dp_worker,
			cp_config_gen,
			function_ectx,
			packet_front
		);
	}

	counter_add_packets_bytes(
		cp_pipeline->counter_packet_out_count,
		cp_pipeline->counter_packet_out_bytes,
		dp_worker->idx,
		storage,
		packet_front->output.count,
		packet_list_bytes_sum(&packet_front->output)
	);
	counter_add_packets_bytes(
		cp_pipeline->counter_packet_drop_count,
		cp_pipeline->counter_packet_drop_bytes,
		dp_worker->idx,
		storage,
		packet_front->drop.count,
		packet_list_bytes_sum(&packet_front->drop)
	);
}

static void
device_entry_ectx_process(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct device_entry_ectx *entry_ectx,
	struct packet_front *packet_front,
	struct packet *packet
) {
	entry_ectx->handler(dp_worker, device_ectx, packet);
	packet->tx_device_id =
		ADDR_OF(&device_ectx->cp_device)->config_item.index;

	if (!entry_ectx->pipeline_map_size) {
		packet->pipeline_ectx = NULL;
		packet_list_add(&packet_front->drop, packet);
		return;
	}
	struct pipeline_ectx *pipeline_ectx =
		ADDR_OF(entry_ectx->pipeline_map +
			packet->hash % entry_ectx->pipeline_map_size);
	if (!pipeline_ectx) {
		packet->pipeline_ectx = NULL;
		packet_list_add(&packet_front->drop, packet);
		return;
	}
	packet->pipeline_ectx = pipeline_ectx;
	packet_list_add(&packet_front->pending, packet);
}

void
device_ectx_process_input(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front,
	struct packet *packet
) {
	struct cp_device *cp_device = ADDR_OF(&device_ectx->cp_device);
	counter_add_packets_bytes(
		cp_device->counter_packet_rx_count,
		cp_device->counter_packet_rx_bytes,
		dp_worker->idx,
		ADDR_OF(&device_ectx->counter_storage),
		1,
		packet_data_len(packet)
	);

	struct device_entry_ectx *entry_ectx =
		ADDR_OF(&device_ectx->input_pipelines);
	device_entry_ectx_process(
		dp_worker, device_ectx, entry_ectx, packet_front, packet
	);
}

void
device_ectx_process_output(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front,
	struct packet *packet
) {
	struct cp_device *cp_device = ADDR_OF(&device_ectx->cp_device);
	counter_add_packets_bytes(
		cp_device->counter_packet_tx_count,
		cp_device->counter_packet_tx_bytes,
		dp_worker->idx,
		ADDR_OF(&device_ectx->counter_storage),
		1,
		packet_data_len(packet)
	);

	struct device_entry_ectx *entry_ectx =
		ADDR_OF(&device_ectx->output_pipelines);
	device_entry_ectx_process(
		dp_worker, device_ectx, entry_ectx, packet_front, packet
	);
}
