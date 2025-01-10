#pragma once

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#include "dataplane/packet/packet.h"

#include "common/memory.h"

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
	packet_list_concat(&packet_front->input, &packet_front->output);
	packet_list_init(&packet_front->output);
}

static inline void
packet_front_pass(struct packet_front *packet_front) {
	packet_list_concat(&packet_front->output, &packet_front->input);
	packet_list_init(&packet_front->input);
}

struct dp_config;
struct module_data;

/*
 * Module handler called for a pipeline front.
 * Module should go through the front and handle packets.
 * For each input packet module should put into output or drop list of the
 * front.
 * Also module may create new packet and put the into output queue.
 */
typedef void (*module_handler)(
	struct dp_config *dp_config,
	struct module_data *module_data,
	struct packet_front *packet_front
);

struct module {
	char name[MODULE_NAME_LEN];
	module_handler handler;
};

typedef struct module *(*module_load_handler)();
