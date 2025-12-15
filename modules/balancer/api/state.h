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

// Capacity of the session table.
size_t
balancer_state_session_table_capacity(struct balancer_state *state);

// Resize sessions table.
// Returns -1 on error and 0 if we successfully resized.
int
balancer_state_resize_session_table(
	struct balancer_state *state, size_t new_size, uint32_t now
);

////////////////////////////////////////////////////////////////////////////////

// Id of the sessions between client and virtual service.
struct balancer_session_id {
	// registry id of the virtual service
	uint32_t vs_id;
	uint8_t client_ip[16]; // client ip in the network byte order
	uint16_t client_port;  // client port in the network byte order
};

// Represents state info of the session between client and virtual service.
struct balancer_session_state {
	// registry id of real which serves session
	uint32_t real_id;
	uint32_t create_timestamp;
	uint32_t last_packet_timestamp;
	uint32_t timeout;
};