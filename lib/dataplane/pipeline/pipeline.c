#include "pipeline.h"

#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"
#include "lib/logging/log.h"

void
pipeline_process(
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	uint64_t worker_idx,
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

	for (uint64_t stage_idx = 0; stage_idx < cp_pipeline->length;
	     ++stage_idx) {
		struct cp_module *cp_module = cp_config_gen_get_module(
			cp_config_gen, pipeline_modules[stage_idx].index
		);

		uint64_t module_index = cp_module->type;
		struct dp_module *dp_module =
			ADDR_OF(&dp_config->dp_modules) + module_index;

		packet_front_switch(packet_front);

		struct counter_storage *counter_storage =
			ADDR_OF(&pipeline_modules[stage_idx].counter_storage);

		dp_module->handler(
			dp_config,
			worker_idx,
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
	}
}
