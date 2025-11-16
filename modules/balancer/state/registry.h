#pragma once

#include <netinet/in.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "worker.h"

#include "../api/info.h"
#include "../dataplane/real.h"

#include "common/interval_counter.h"

////////////////////////////////////////////////////////////////////////////////

// Persistent state of the service (virtual or real).
// Sharded between workers.
struct service_state {
	// used to track active connections
	struct interval_counter active_sessions;

	// last packet timestamp
	uint32_t last_packet_timestamp;

	union {
		struct balancer_real_stats real;
		struct balancer_vs_stats vs;
	} stats;
} __attribute__((__aligned__(64)));

void
service_state_copy(struct service_state *dst, struct service_state *src);

////////////////////////////////////////////////////////////////////////////////

// Info about virtual or real service.
struct service_info {
	// address of the virtual service
	uint8_t vip_address[16];

	// type of vip address
	int vip_proto;

	// destination ip address (equals to vip in case of virtual service)
	uint8_t ip_address[16];

	// type of ip address
	int ip_proto; // IPPROTO_IPV4 or IPPROTO_IPV6

	// zero in case of pure l3 scheduling
	uint16_t port;

	// tcp or udp
	int transport_proto; // IPPROTO_TCP or IPPROTO_UDP

	// per worker service state
	struct service_state state[MAX_WORKERS_NUM];
};

struct balancer_real_info;
struct balancer_vs_info;

void
service_info_accumulate_into_real_info(
	struct service_info *service_info,
	struct balancer_real_info *real_info,
	size_t workers
);

void
service_info_accumulate_into_vs_info(
	struct service_info *service_info,
	struct balancer_vs_info *vs_info,
	size_t workers
);

////////////////////////////////////////////////////////////////////////////////

struct service_registry {
	size_t service_count;
	struct service_info *services;
};

////////////////////////////////////////////////////////////////////////////////

static inline void
service_state_put_session(
	struct service_state *state,
	uint32_t now,
	uint32_t from,
	uint32_t timeout
) {
	interval_counter_put(&state->active_sessions, from, timeout, 1);
	state->last_packet_timestamp = now;
}

static inline void
service_state_update(struct service_state *state, uint32_t now) {
	interval_counter_advance_time(&state->active_sessions, now);
}