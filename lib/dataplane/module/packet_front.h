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
 */
struct packet_front {
	struct packet_list pending_input;
	struct packet_list pending_output;

	struct packet_list input;
	struct packet_list output;
	struct packet_list drop;

	uint64_t pending_input_count;
	uint64_t pending_input_bytes;

	uint64_t pending_output_count;
	uint64_t pending_output_bytes;

	uint64_t input_count;
	uint64_t input_bytes;

	uint64_t output_count;
	uint64_t output_bytes;

	uint64_t drop_count;
	uint64_t drop_bytes;
};

static inline void
packet_front_init(struct packet_front *packet_front) {
	packet_list_init(&packet_front->pending_input);
	packet_list_init(&packet_front->pending_output);
	packet_list_init(&packet_front->input);
	packet_list_init(&packet_front->output);
	packet_list_init(&packet_front->drop);

	packet_front->pending_input_count = 0;
	packet_front->pending_input_bytes = 0;
	packet_front->pending_output_count = 0;
	packet_front->pending_output_bytes = 0;
	packet_front->input_count = 0;
	packet_front->input_bytes = 0;
	packet_front->output_count = 0;
	packet_front->output_bytes = 0;
	packet_front->drop_count = 0;
	packet_front->drop_bytes = 0;
}

// Resets a fully-drained front to a clean reusable state.
//
// Only valid on a front whose input, output and pending lists have already
// been drained to empty by a completed worker round. Resets the spent drop
// list (its mbufs were already freed by the caller, so only the head needs
// resetting) and the stale pending/drop accumulator counters, so the front
// can be handed to the next round without a full packet_front_init.
static inline void
packet_front_recycle(struct packet_front *packet_front) {
	packet_list_init(&packet_front->drop);

	packet_front->drop_count = 0;
	packet_front->drop_bytes = 0;
	packet_front->pending_input_count = 0;
	packet_front->pending_input_bytes = 0;
	packet_front->pending_output_count = 0;
	packet_front->pending_output_bytes = 0;
}

// Move the whole output, drop and pending lists from src into dst, transferring
// the counters, and leave src empty.
//
// Zeroing src lets a scratch front be reused across rounds without a separate
// per-round reset: merge moves every packet and counter out, so nothing stale
// survives into the next round.
static inline void
packet_front_merge(struct packet_front *dst, struct packet_front *src) {
	packet_list_concat(&dst->output, &src->output);
	packet_list_concat(&dst->drop, &src->drop);
	packet_list_concat(&dst->pending_input, &src->pending_input);
	packet_list_concat(&dst->pending_output, &src->pending_output);

	dst->pending_input_count += src->pending_input_count;
	dst->pending_input_bytes += src->pending_input_bytes;
	dst->pending_output_count += src->pending_output_count;
	dst->pending_output_bytes += src->pending_output_bytes;
	dst->output_count += src->output_count;
	dst->output_bytes += src->output_bytes;
	dst->drop_count += src->drop_count;
	dst->drop_bytes += src->drop_bytes;

	packet_front_init(src);
}

static inline void
packet_front_pending_input(
	struct packet_front *packet_front, struct packet *packet
) {
	packet_list_add(&packet_front->pending_input, packet);
	packet_front->pending_input_count += 1;
	packet_front->pending_input_bytes += packet->data_len;
}

static inline void
packet_front_pending_output(
	struct packet_front *packet_front, struct packet *packet
) {
	packet_list_add(&packet_front->pending_output, packet);
	packet_front->pending_output_count += 1;
	packet_front->pending_output_bytes += packet->data_len;
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

// Move the whole output list of src into dst, transferring the output
// counters.
//
// Used by the single-pipeline fast path that schedules a front on a fresh
// packet_front for a stage that reads output.
static inline void
packet_front_take_output(struct packet_front *dst, struct packet_front *src) {
	packet_list_concat(&dst->output, &src->output);

	dst->output_count = src->output_count;
	dst->output_bytes = src->output_bytes;
	src->output_count = 0;
	src->output_bytes = 0;
}

// Move src's output list into dst's input, copying src's output counters to
// dst's input counters.
//
// Used by the single-chain fast path that schedules a front on a fresh
// packet_front so module 0 reads input directly without a prior switch.
static inline void
packet_front_take_input(struct packet_front *dst, struct packet_front *src) {
	packet_list_concat(&dst->input, &src->output);

	dst->input_count = src->output_count;
	dst->input_bytes = src->output_bytes;
	src->output_count = 0;
	src->output_bytes = 0;
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

// Move the whole pending_input list into the drop list, transferring the
// counters.
//
// Used by drain paths that discard all pending input (e.g. when no
// pipeline is configured).
static inline void
packet_front_drop_pending_input(struct packet_front *packet_front) {
	packet_list_concat(&packet_front->drop, &packet_front->pending_input);

	packet_front->drop_count += packet_front->pending_input_count;
	packet_front->drop_bytes += packet_front->pending_input_bytes;
	packet_front->pending_input_count = 0;
	packet_front->pending_input_bytes = 0;
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

static inline uint64_t
packet_front_pending_input_count(struct packet_front *packet_front) {
	return packet_front->pending_input_count;
}

static inline uint64_t
packet_front_pending_input_bytes(struct packet_front *packet_front) {
	return packet_front->pending_input_bytes;
}

static inline uint64_t
packet_front_pending_output_count(struct packet_front *packet_front) {
	return packet_front->pending_output_count;
}

static inline uint64_t
packet_front_pending_output_bytes(struct packet_front *packet_front) {
	return packet_front->pending_output_bytes;
}
