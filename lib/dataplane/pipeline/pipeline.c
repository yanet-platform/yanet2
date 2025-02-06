#include "pipeline.h"

#include <stdlib.h>
#include <string.h>

#include "dataplane/module/module.h"

void
pipeline_process(struct pipeline *pipeline, struct packet_front *packet_front) {
	for (uint32_t idx = 0; idx < pipeline->module_config_count; ++idx) {
		struct module_config *module_config =
			pipeline->module_configs[idx];

		// Connect previous output to the next input. */
		packet_front_switch(packet_front);

		// Invoke module instance.
		module_process(module_config, packet_front);
	}
}

int
pipeline_configure(
	const char *name,
	struct module_config **module_configs,
	uint32_t module_config_count,
	struct pipeline **pipeline
) {

	struct pipeline *new_pipeline = (struct pipeline *)malloc(
		sizeof(struct pipeline) +
		sizeof(struct module_config *) * module_config_count
	);

	if (new_pipeline == NULL)
		return -1;

	stpncpy(new_pipeline->name, name, PIPELINE_NAME_LEN);
	new_pipeline->name[PIPELINE_NAME_LEN - 1] = '\0';

	for (uint32_t idx = 0; idx < module_config_count; ++idx) {
		new_pipeline->module_configs[idx] = module_configs[idx];
		++new_pipeline->module_configs[idx]->ref_count;
	}

	new_pipeline->module_config_count = module_config_count;
	*pipeline = new_pipeline;

	return 0;
}
