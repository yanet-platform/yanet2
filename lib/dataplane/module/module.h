#pragma once

#include <stdint.h>

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
	struct packet_list pending;
	struct packet_list input;
	struct packet_list output;
	struct packet_list drop;
};

static inline void
packet_front_init(struct packet_front *packet_front) {
	packet_list_init(&packet_front->pending);
	packet_list_init(&packet_front->input);
	packet_list_init(&packet_front->output);
	packet_list_init(&packet_front->drop);
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
struct module_ectx;
struct dp_worker;
struct counter_storage;

/*
 * Module handler called for a pipeline front.
 * Module should go through the front and handle packets.
 * For each input packet module should put into output or drop list of the
 * front.
 * Also module may create new packet and put the into output queue.
 */
typedef void (*module_handler)(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

struct module {
	char name[MODULE_NAME_LEN];
	module_handler handler;
};

typedef struct module *(*module_load_handler)();

// FIXME move the code bellow to a separate file
#define DEVICE_NAME_LEN 80
struct device_ectx;

typedef void (*device_handler)(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet *packet
);

struct device {
	char name[DEVICE_NAME_LEN];
	device_handler input_handler;
	device_handler output_handler;
};

typedef struct device *(*device_load_handler)();
