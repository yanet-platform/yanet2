#include "pipeline.h"

#include <stdlib.h>
#include <string.h>

#include "dataplane/module/module.h"

#include "dataplane/config/zone.h"

void
pipeline_process(
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	uint64_t pipeline_idx,
	struct packet_front *packet_front
) {
	struct cp_pipeline_registry *cp_pipeline_registry =
		DECODE_ADDR(cp_config_gen, cp_config_gen->pipeline_registry);

	if (pipeline_idx >= cp_pipeline_registry->count) {
		packet_list_concat(&packet_front->drop, &packet_front->output);
		packet_list_init(&packet_front->output);
		return;
	}

	struct cp_pipeline *cp_pipeline =
		cp_pipeline_registry->pipelines + pipeline_idx;
	uint64_t *module_indexes =
		DECODE_ADDR(cp_pipeline, cp_pipeline->module_indexes);

	struct cp_module_registry *cp_module_registry =
		DECODE_ADDR(cp_config_gen, cp_config_gen->module_registry);

	for (uint64_t stage_idx = 0; stage_idx < cp_pipeline->length;
	     ++stage_idx) {
		struct cp_module *cp_module =
			cp_module_registry->modules + module_indexes[stage_idx];

		uint64_t module_index =
			DECODE_ADDR(cp_module, cp_module->data)->index;
		struct dp_module *dp_module =
			DECODE_ADDR(dp_config, dp_config->dp_modules) +
			module_index;

		packet_front_switch(packet_front);

		dp_module->handler(
			dp_config,
			DECODE_ADDR(cp_module, cp_module->data),
			packet_front
		);
	}
}
