#pragma once

#include "lib/dataplane/packet/packet.h"

/*
 * Packets processed by a pipeline stage. A module reads incoming packets
 * from the input list and writes results to the output or drop list.
 *
 * input is the stage entry list and output the emission list. The entry
 * push (packet_front_input) sets the input counters directly — the only
 * switch is the inter-module handoff turning module N's output into
 * module N+1's input.
 *
 * The counters are the tallies the stage boundaries around a handler
 * read: rx/tx/drop per module inside a chain, and the entry tallies
 * around a device handler. Everywhere else the pipeline moves bare
 * packet lists and credits the per-stage counters directly, so a front
 * lives only where a handler runs.
 */
struct packet_front {
	struct packet_list input;
	struct packet_list output;
	struct packet_list drop;

	uint64_t input_count;
	uint64_t input_bytes;

	uint64_t output_count;
	uint64_t output_bytes;

	uint64_t drop_count;
	uint64_t drop_bytes;
};

static inline void
packet_front_init(struct packet_front *packet_front) {
	packet_list_init(&packet_front->input);
	packet_list_init(&packet_front->output);
	packet_list_init(&packet_front->drop);

	packet_front->input_count = 0;
	packet_front->input_bytes = 0;
	packet_front->output_count = 0;
	packet_front->output_bytes = 0;
	packet_front->drop_count = 0;
	packet_front->drop_bytes = 0;
}

static inline void
packet_front_input(struct packet_front *packet_front, struct packet *packet) {
	packet_list_add(&packet_front->input, packet);
	packet_front->input_count += 1;
	packet_front->input_bytes += packet->data_len;
}

static inline void
packet_front_output(struct packet_front *packet_front, struct packet *packet) {
	packet_list_add(&packet_front->output, packet);
	packet_front->output_count += 1;
	packet_front->output_bytes += packet->data_len;
}

static inline void
packet_front_drop(struct packet_front *packet_front, struct packet *packet) {
	packet_list_add(&packet_front->drop, packet);
	packet_front->drop_count += 1;
	packet_front->drop_bytes += packet->data_len;
}

// Collects up to the given number of packets from the front's input
// list without consuming them.
//
// A handler working through an unbounded front in fixed batches
// collects a batch, classifies it, and then consumes exactly that many
// packets from the head; the remainder stays listed for the next batch.
static inline uint32_t
packet_front_collect_input(
	struct packet_front *packet_front,
	struct packet **packets,
	uint32_t capacity
) {
	uint32_t count = 0;
	for (struct packet *packet = packet_list_first(&packet_front->input);
	     packet != NULL && count < capacity;
	     packet = packet->next) {
		packets[count++] = packet;
	}
	return count;
}

// Sum the packets and first-segment bytes of a bare list in one walk.
//
// The only producer handing a bare list to a front is the device entry
// detach: the inbox carries no counters, so the active front tallies
// its input here. The walk touches the packet heads the device handler
// is about to read anyway.
static inline void
packet_list_tally(struct packet_list *list, uint64_t *count, uint64_t *bytes) {
	uint64_t total_count = 0;
	uint64_t total_bytes = 0;
	for (struct packet *packet = packet_list_first(list); packet != NULL;
	     packet = packet->next) {
		total_count += 1;
		total_bytes += packet->data_len;
	}
	*count = total_count;
	*bytes = total_bytes;
}

// Inter-module handoff: move output into input for the next module. Stage
// entry is by packet_front_input, not a switch.
static inline void
packet_front_switch(struct packet_front *packet_front) {
	packet_list_concat(&packet_front->input, &packet_front->output);

	packet_front->input_count = packet_front->output_count;
	packet_front->input_bytes = packet_front->output_bytes;
	packet_front->output_count = 0;
	packet_front->output_bytes = 0;
}

static inline void
packet_front_pass(struct packet_front *packet_front) {
	packet_list_concat(&packet_front->output, &packet_front->input);

	packet_front->output_count += packet_front->input_count;
	packet_front->output_bytes += packet_front->input_bytes;
	packet_front->input_count = 0;
	packet_front->input_bytes = 0;
}

// Move the whole output list into the drop list, transferring the counters.
//
// Used by drain paths that discard unroutable output.
static inline void
packet_front_drop_output(struct packet_front *packet_front) {
	packet_list_concat(&packet_front->drop, &packet_front->output);

	packet_front->drop_count += packet_front->output_count;
	packet_front->drop_bytes += packet_front->output_bytes;
	packet_front->output_count = 0;
	packet_front->output_bytes = 0;
}

static inline uint64_t
packet_front_input_count(struct packet_front *packet_front) {
	return packet_front->input_count;
}

static inline uint64_t
packet_front_input_bytes(struct packet_front *packet_front) {
	return packet_front->input_bytes;
}

static inline uint64_t
packet_front_output_count(struct packet_front *packet_front) {
	return packet_front->output_count;
}

static inline uint64_t
packet_front_output_bytes(struct packet_front *packet_front) {
	return packet_front->output_bytes;
}

static inline uint64_t
packet_front_drop_count(struct packet_front *packet_front) {
	return packet_front->drop_count;
}

static inline uint64_t
packet_front_drop_bytes(struct packet_front *packet_front) {
	return packet_front->drop_bytes;
}
