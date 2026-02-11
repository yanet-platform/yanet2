#pragma once

#include "lib/dataplane/packet/packet.h"

/*
 * The structure enumerated packets processed by pipeline modules.
 * Each module reads a packet from an input list and then writes result to
 * an output list or drop list.
 *
 * Before module invocation input and output exchange packets so ouptut of
 * one module connects with input of the following.
 *
 * RX and TX are considered as separated stages of packet processing working
 * before and after pipeline processing.
 */
struct packet_front {
	struct packet_list pending_input;
	struct packet_list pending_output;

	struct packet_list input;
	struct packet_list output;
	struct packet_list drop;
};

static inline void
packet_front_init(struct packet_front *packet_front) {
	packet_list_init(&packet_front->pending_input);
	packet_list_init(&packet_front->pending_output);
	packet_list_init(&packet_front->input);
	packet_list_init(&packet_front->output);
	packet_list_init(&packet_front->drop);
}

static inline void
packet_front_merge(struct packet_front *dst, struct packet_front *src) {
	packet_list_concat(&dst->output, &src->output);
	packet_list_concat(&dst->drop, &src->drop);
	packet_list_concat(&dst->pending_input, &src->pending_input);
	packet_list_concat(&dst->pending_output, &src->pending_output);
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
