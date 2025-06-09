#include "pipeline.h"

#include "counters/utils.h"

#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"
#include "lib/logging/log.h"

#include <rte_cycles.h>

void
pipeline_process(
	struct dp_config *dp_config,
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	uint64_t pipeline_idx,
	struct packet_front *packet_front
) {
	struct cp_pipeline *cp_pipeline =
		cp_config_gen_get_pipeline(cp_config_gen, pipeline_idx);
	if (cp_pipeline == NULL) {
		packet_list_concat(&packet_front->drop, &packet_front->output);
		packet_list_init(&packet_front->output);
		return;
	}

	struct cp_pipeline_module *pipeline_modules = cp_pipeline->modules;

	*counter_get_address(
		cp_pipeline->counter_packet_in_count,
		dp_worker->idx,
		ADDR_OF(&cp_pipeline->counters)
	) += packet_list_count(&packet_front->output);

	counter_hist_exp2_inc(
		cp_pipeline->counter_packet_in_hist,
		dp_worker->idx,
		ADDR_OF(&cp_pipeline->counters),
		0,
		7,
		packet_list_count(&packet_front->output),
		1
	);

	uint64_t tsc_start = rte_rdtsc();

	for (uint64_t stage_idx = 0; stage_idx < cp_pipeline->length;
	     ++stage_idx) {
		struct cp_module *cp_module = cp_config_gen_get_module(
			cp_config_gen, pipeline_modules[stage_idx].index
		);

		uint64_t module_index = cp_module->type;
		struct dp_module *dp_module =
			ADDR_OF(&dp_config->dp_modules) + module_index;

		packet_front_switch(packet_front);

		uint64_t input_size = packet_list_count(&packet_front->input);

		struct counter_storage *counter_storage =
			ADDR_OF(&pipeline_modules[stage_idx].counter_storage);

		dp_module->handler(
			dp_config,
			dp_worker->idx,
			cp_module,
			counter_storage,
			packet_front
		);

		LOG_TRACEX(int in = packet_list_counter(&packet_front->input);
			   int out = packet_list_counter(&packet_front->output);
			   int bypass =
				   packet_list_counter(&packet_front->bypass);
			   int drop = packet_list_counter(&packet_front->drop);
			   packet_list_print(&packet_front->output);
			   ,
			   "processed packets with module %s, in %d, out "
			   "%d, bypass %d, drop %d. Output list printed above.",
			   dp_module->name,
			   in,
			   out,
			   bypass,
			   drop);

		uint64_t tsc_stop = rte_rdtsc();
		counter_hist_exp2_inc(
			pipeline_modules[stage_idx].tsc_counter_id,
			dp_worker->idx,
			ADDR_OF(&cp_pipeline->counters),
			0,
			7,
			input_size,
			tsc_stop - tsc_start
		);
		tsc_start = tsc_stop;
	}

	*counter_get_address(
		cp_pipeline->counter_packet_out_count,
		dp_worker->idx,
		ADDR_OF(&cp_pipeline->counters)
	) += packet_list_count(&packet_front->output);

	*counter_get_address(
		cp_pipeline->counter_packet_drop_count,
		dp_worker->idx,
		ADDR_OF(&cp_pipeline->counters)
	) += packet_list_count(&packet_front->drop);

	*counter_get_address(
		cp_pipeline->counter_packet_bypass_count,
		dp_worker->idx,
		ADDR_OF(&cp_pipeline->counters)
	) += packet_list_count(&packet_front->bypass);
}
