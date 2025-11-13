#pragma once

#include <netinet/in.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "common/interval_counter.h"

#include "worker.h"

////////////////////////////////////////////////////////////////////////////////

// sharded between workers
struct service_state {
	struct interval_counter active_sessions;
	uint32_t last_seen;
} __attribute__((__aligned__(64)));

void
service_state_copy(struct service_state *dst, struct service_state *src);

////////////////////////////////////////////////////////////////////////////////

// Info about virtual service or real.
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
	state->last_seen = now;
}

static inline void
service_state_update(struct service_state *state, uint32_t now) {
	interval_counter_advance_time(&state->active_sessions, now);
}