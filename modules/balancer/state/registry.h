#pragma once

#include <netinet/in.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "common/interval_counter.h"

#include "worker.h"

////////////////////////////////////////////////////////////////////////////////

struct service_state {
	struct interval_counter active_sessions;
	uint32_t last_seen;
} __attribute__((__aligned__(64))); // because of sharded between workers

void
service_state_copy(struct service_state *dst, struct service_state *src);

////////////////////////////////////////////////////////////////////////////////

struct service_info {
	uint8_t ip_address[16];
	int ip_proto;	     // IPPROTO_IPV4 or IPPROTO_IPV6
	uint16_t port;	     // does not matter for reals
	int transport_proto; // IPPROTO_TCP or IPPROTO_UDP
	struct service_state state[MAX_WORKERS_NUM]; // per worker service state
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