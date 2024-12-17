#pragma once

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#include "dataplane/packet/packet.h"

#define MODULE_NAME_LEN 80
#define MODULE_CONFIG_NAME_LEN 80

/*
 * The structure enumerated packets processed by pipeline modules.
 * Each module reads a packet from an input list and then writes result to
 * an output list or bypass the pipeline landing the packet to a send or drop
 * list.
 *
 * Before module invocation input and output exchange packets so ouptut of
 * one module connects with input of the following.
 *
 * RX and TX are considered as separated stages of packet processing working
 * before and after pipeline processing.
 */
struct packet_front {
	struct packet_list input;
	struct packet_list output;
	struct packet_list drop;
	struct packet_list bypass;
};

static inline void
packet_front_init(struct packet_front *packet_front) {
	packet_list_init(&packet_front->input);
	packet_list_init(&packet_front->output);
	packet_list_init(&packet_front->drop);
	packet_list_init(&packet_front->bypass);
}

static inline void
packet_front_output(struct packet_front *packet_front, struct packet *packet) {
	packet_list_add(&packet_front->output, packet);
}

static inline void
packet_front_drop(struct packet_front *packet_front, struct packet *packet) {
	packet_list_add(&packet_front->drop, packet);
}

static inline void
packet_front_bypass(struct packet_front *packet_front, struct packet *packet) {
	packet_list_add(&packet_front->bypass, packet);
}

static inline void
packet_front_switch(struct packet_front *packet_front) {
	packet_front->input = packet_front->output;
	packet_list_init(&packet_front->output);
}

struct module;
struct module_config;

/*
 * Module handler called for a pipeline front.
 * Module should go through the front and handle packets.
 * For each input packet module should put into output or drop list of the
 * front.
 * Also module may create new packet and put the into output queue.
 */
typedef void (*module_handler)(
	struct module *module,
	struct module_config *module_config,
	struct packet_front *packet_front
);

/*
 * The module configuration handler called when module should be created,
 * reconfigured and freed. The handler accepts raw configuration data,
 * old instance configuration (or NULL) and sets new configuration pointer
 * via output parameter.
 *
 * The handler is responsible for:
 *  - checking if the configuration is same
 *  - preserving runtime parameters and variables
 */

typedef int (*module_config_handler)(
	struct module *module,
	const void *config_data,
	size_t config_data_size,
	struct module_config **new_config
);

struct module {
	char name[MODULE_NAME_LEN];
	module_handler handler;
	module_config_handler config_handler;
};

struct module_config {
	struct module *module;
	char name[MODULE_CONFIG_NAME_LEN];
	uint32_t ref_count;
};

static inline void
module_process(
	struct module_config *config, struct packet_front *packet_front
) {
	return config->module->handler(config->module, config, packet_front);
}

static inline int
module_configure(
	struct module *module,
	const char *config_name,
	const void *config_data,
	size_t config_data_size,
	struct module_config *old_config,
	struct module_config **new_config
) {
	(void)old_config;

	int ret = module->config_handler(
		module, config_data, config_data_size, new_config
	);

	if (ret)
		return ret;

	(*new_config)->module = module;

	snprintf(
		(*new_config)->name,
		sizeof((*new_config)->name),
		"%s",
		config_name
	);

	(*new_config)->ref_count = 1;

	return 0;
}

typedef struct module *(*module_load_handler)();
