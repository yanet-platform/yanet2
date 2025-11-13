#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <sys/types.h>

////////////////////////////////////////////////////////////////////////////////

struct balancer_state;
struct agent;

////////////////////////////////////////////////////////////////////////////////

struct balancer_state *
balancer_state_create(
	struct agent *agent,
	size_t table_size,
	uint32_t tcp_syn_ack_timeout,
	uint32_t tcp_syn_timeout,
	uint32_t tcp_fin_timeout,
	uint32_t tcp_timeout,
	uint32_t udp_timeout,
	uint32_t default_timeout
);

void
balancer_state_destroy(struct balancer_state *state);

////////////////////////////////////////////////////////////////////////////////

ssize_t
balancer_state_register_vs(
	struct balancer_state *state,
	uint64_t flags,
	uint8_t *ip_address,
	uint16_t port,
	int transport_proto
);

ssize_t
balancer_state_register_real(
	struct balancer_state *state,
	uint8_t *vip_address,
	uint64_t virtual_flags,
	uint16_t port,
	int transport_proto,
	uint64_t real_flags,
	uint8_t *ip_address
);

////////////////////////////////////////////////////////////////////////////////

int
balancer_state_extend_session_table(struct balancer_state *state, bool force);

int
balancer_state_gc_session_table(struct balancer_state *state);
