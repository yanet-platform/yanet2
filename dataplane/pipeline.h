#pragma once

#include <stdint.h>

#include "module.h"
#include "packet/packet.h"

struct pipeline;

/*
 * The structure enumerated packets processed by pipeline modules.
 * Each module reads a packet from an input list and then writes result to
 * an output list or bypass the pipeline landing the packet to a drop list.
 *
 * Before module invocation input and output exchange packets so ouptut of
 * one module connects with input of the following.
 *
 * RX and TX are considered as separated stages of packet processing working
 * before and after pipeline processing.
 */
struct pipeline_front {
	struct packet_list input;
	struct packet_list output;
	struct packet_list drop;
	struct packet_list bypass;
};

static inline void
pipeline_front_init(struct pipeline_front *pipeline_front) {
	packet_list_init(&pipeline_front->input);
	packet_list_init(&pipeline_front->output);
	packet_list_init(&pipeline_front->drop);
	packet_list_init(&pipeline_front->bypass);
}

static inline void
pipeline_front_output(
	struct pipeline_front *pipeline_front, struct packet *packet
) {
	packet_list_add(&pipeline_front->output, packet);
}

static inline void
pipeline_front_drop(
	struct pipeline_front *pipeline_front, struct packet *packet
) {
	packet_list_add(&pipeline_front->drop, packet);
}

static inline void
pipeline_front_bypass(
	struct pipeline_front *pipeline_front, struct packet *packet
) {
	packet_list_add(&pipeline_front->bypass, packet);
}

static inline void
pipeline_front_switch(struct pipeline_front *pipeline_front) {
	pipeline_front->input = pipeline_front->output;
	packet_list_init(&pipeline_front->output);
}

/*
 * DRAFT.
 * Module configuration data denotes module and instance name and a pointer
 * to configuration values which the module should decode and apply.
 */
struct pipeline_module_config_ref {
	char module_name[MODULE_NAME_LEN];
	char config_name[MODULE_CONFIG_NAME_LEN];
};

struct pipeline_module_config;

/*
 * DRAFT.
 * Module instance handler inside pipeline contains
 *  - module handler used to invoke module functions
 *  - module instance configuration used to pass into module functions.
 * Each module could have multiple instances differing in configuration
 * runtime parameters and data so the module configuration contains all data
 * required to manage the module instance.
 */
struct pipeline_module_config {
	struct pipeline_module_config *next;
	struct module *module;
	struct module_config *config;
};

/*
 * Pipeline contains module instances list calling one by one for
 * each pipeline front of packets.
 **/
struct pipeline {
	struct pipeline_module_config *module_configs;
};

struct pipeline *
pipeline_create();

int
pipeline_init(struct pipeline *pipeline);

/*
 * Pipeline configuration routine.
 * The function rebuilds the pipeline with new module list and module
 * configuration.
 *
 * NOTE:
 * Pipeline front processing shoulnd not be affected by the routine.
 */
int
pipeline_configure(
	struct pipeline *pipeline,
	struct pipeline_module_config_ref *module_config_refs,
	uint32_t config_size,
	struct module_registry *module_registry
);

/*
 * Drives piepline front through pipeline modules.
 *
 * NOTE: Pipeline processing assumes all RX are placed to output list of
 * pipeline front as the RX is a stage of the pipeline. Also pipeline outputs
 * will be placed to output list and packet dropped while processing to
 * drop list.
 */
void
pipeline_process(
	struct pipeline *pipeline, struct pipeline_front *pipeline_front
);
