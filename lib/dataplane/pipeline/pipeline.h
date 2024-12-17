#pragma once

#include <stdint.h>

#include "dataplane/module/module.h"

#define PIPELINE_NAME_LEN 80

/*
 * Pipeline contains module instances list calling one by one for
 * each pipeline front of packets.
 **/
struct pipeline {
	/*
	 * FIXME: this may break cache line prefetch - should we use
	 * pointers instead and place the content just after the module
	 * configuration array (as we already use variable-length
	 * allocation for the structure).
	 */
	char name[PIPELINE_NAME_LEN];
	uint32_t module_config_count;
	struct module_config *module_configs[0];
};

/*
 * Pipeline configuration routine.
 * The function rebuilds the pipeline with new module list and module
 * configuration.
 *
 * NOTE:
 * Pipeline front processing shoulnd not be affected by the routine.
 */
/*
 * FIXME: create new pipeline instance as it allows us to make pipeline
 * switch transactional
 */
int
pipeline_configure(
	const char *name,
	struct module_config **module_configs,
	uint32_t module_config_count,
	struct pipeline **pipeline
);

/*
 * Drives packet front through pipeline modules.
 *
 * NOTE: Pipeline processing assumes all RX are placed to output list of
 * pipeline front as the RX is a stage of the pipeline. Also pipeline outputs
 * will be placed to output list and packet dropped while processing to
 * drop list.
 */
void
pipeline_process(struct pipeline *pipeline, struct packet_front *packet_front);
