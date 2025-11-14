#pragma once

#include "../api/info.h"

#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

// Some numeric statistics for the balancer module config
struct module_config_counter {
	uint64_t incoming_packets;
	uint64_t incoming_bytes;
	uint64_t select_vs_failed;
	uint64_t invalid_packets;
	uint64_t select_real_failed;
	uint64_t tunnel_failed;
	uint64_t outgoing_packets;
	uint64_t outgoing_bytes;
};

#define MODULE_CONFIG_COUNTER_SIZE                                             \
	((sizeof(struct module_config_counter) / sizeof(uint64_t)))

// New packet accepted
static inline void
module_config_counter_incoming_packet(
	struct module_config_counter *counter, size_t packet_len
) {
	counter->incoming_packets += 1;
	counter->incoming_bytes += packet_len;
}

static inline void
module_config_counter_outgoing_packet(
	struct module_config_counter *counter, size_t packet_len
) {
	counter->outgoing_packets += 1;
	counter->outgoing_bytes += packet_len;
}

////////////////////////////////////////////////////////////////////////////////

typedef struct balancer_vs_stats vs_counter_t;

#define VS_COUNTER_SIZE ((sizeof(vs_counter_t) / sizeof(uint64_t)))

////////////////////////////////////////////////////////////////////////////////

// On new packet

static inline void
vs_counter_incoming_packet(vs_counter_t *vs_counter, size_t pkt_len) {
	vs_counter->incoming_packets += 1;
	vs_counter->incoming_bytes += pkt_len;
}

static inline void
vs_counter_outgoing_packet(vs_counter_t *vs_counter, size_t pkt_len) {
	vs_counter->outgoing_packets += 1;
	vs_counter->outgoing_bytes += pkt_len;
}

////////////////////////////////////////////////////////////////////////////////

typedef struct balancer_real_stats real_counter_t;

#define REAL_COUNTER_SIZE ((sizeof(real_counter_t) / sizeof(uint64_t)))

////////////////////////////////////////////////////////////////////////////////

// On new packet

static inline void
real_counter_incoming_packet(
	struct balancer_real_stats *real_counter, size_t pkt_len
) {
	real_counter->packets += 1;
	real_counter->bytes += pkt_len;
}

////////////////////////////////////////////////////////////////////////////////
