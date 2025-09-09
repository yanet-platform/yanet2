#include "pipeline.h"

#include "counters/utils.h"

#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"
#include "lib/logging/log.h"

#include <rte_cycles.h>

void
module_ectx_process(
	struct dp_config *dp_config,
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front

) {
	(void)cp_config_gen;

	struct cp_module *cp_module = ADDR_OF(&module_ectx->module);
	struct dp_module *dp_module =
		ADDR_OF(&dp_config->dp_modules) + cp_module->dp_module_idx;

	dp_module->handler(
		dp_config,
		dp_worker->idx,
		cp_module,
		ADDR_OF(&module_ectx->counter_storage),
		packet_front
	);

	LOG_TRACEX(int in = packet_list_counter(&packet_front->input);
		   int out = packet_list_counter(&packet_front->output);
		   int bypass = packet_list_counter(&packet_front->bypass);
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
			ADDR_OF(chain_ectx->modules + idx);

		module_ectx_process(
			dp_config,
			dp_worker,
			cp_config_gen,
			module_ectx,
			packet_front
		);

		if (0) {
			uint64_t tsc_stop = rte_rdtsc();
			counter_hist_exp2_inc(
				0, // module_ectx->tsc_counter_id,
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
}

void
function_ectx_process(
	struct dp_config *dp_config,
	struct dp_worker *dp_worker,
	struct cp_config_gen *cp_config_gen,
	struct function_ectx *function_ectx,
	struct packet_front *packet_front
) {
	// FIXME route through chains
	uint64_t chain_idx = 0;
	struct chain_ectx *chain_ectx =
		ADDR_OF(function_ectx->chain_map + chain_idx);
	chain_ectx_process(
		dp_config, dp_worker, cp_config_gen, chain_ectx, packet_front
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
}
