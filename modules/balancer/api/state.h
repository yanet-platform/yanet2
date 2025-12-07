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
balancer_state_create(struct agent *agent, size_t table_size);

void
balancer_state_destroy(struct balancer_state *state);

////////////////////////////////////////////////////////////////////////////////

/// Register virtual service in the module state registry.
ssize_t
balancer_state_register_vs(
	struct balancer_state *state,
	int transport_proto,
	int network_proto,
	uint8_t *ip_address,
	uint16_t port
);

/// Register real in the module state registry.
ssize_t
balancer_state_register_real(
	struct balancer_state *state,
	int transport_proto,
	int vip_network_proto,
	uint8_t *vip_address,
	uint16_t port,
	int real_network_proto,
	uint8_t *ip_address
);

////////////////////////////////////////////////////////////////////////////////

int
balancer_state_extend_session_table(struct balancer_state *state, bool force);

int
balancer_state_gc_session_table(struct balancer_state *state);

////////////////////////////////////////////////////////////////////////////////

struct balancer_session_id {
	uint8_t transport_proto;
	uint8_t network_proto;

	uint8_t ip_source[16];
	uint8_t ip_destination[16];

	uint16_t port_source;
	uint16_t port_destination;
};

struct balancer_session_state {
	uint32_t real_id; // registry id of real
	uint32_t create_timestamp;
	uint32_t last_packet_timestamp;
	uint32_t timeout;
};