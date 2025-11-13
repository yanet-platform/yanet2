#pragma once

#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

// Balancer state
struct balancer_state;

////////////////////////////////////////////////////////////////////////////////

/// Persistent config-independent info about virtual service
struct balancer_vs_info {
	// ip
	uint8_t ip[16];
	int ip_proto; // IPPROTO_IPV4 or IPPROTO_IPV6

	// port of the virtual service
	// zero if PURE_L3 flag enabled
	uint16_t virtual_port;

	// virtual service transport protocol
	int transport_proto; // IPPROTO_TCP or IPPROTO_UDP

	// number of active connections
	size_t active_connections;

	// last packet timestamp
	uint32_t last_packet_timestamp;

	// number of created connections so far
	size_t created_connections;

	// number of incoming packets
	size_t in_packets;

	// number of outgoing packets
	size_t out_packets;

	// number of incoming traffic bytes
	size_t in_bytes;

	// number of outgoing traffic bytes
	size_t out_bytes;

	// number of packets which were denied because
	// of packet src address not allowed
	size_t denied_packets;

	// number of packets which were discarded because
	// it is impossible to determine destination real
	size_t discarded_packets;

	// number of packets which
	// are dropped because its destination real is disabled
	size_t dropped_packets;
};

struct balancer_virtual_services_info {
	size_t count;
	struct balancer_vs_info *info;
};

/// Fills virtual services info.
/// @returns -1 on error.
int
balancer_fill_vs_info(
	struct balancer_state *state,
	struct balancer_virtual_services_info *info
);

void
balancer_free_vs_info(
	struct balancer_state *state,
	struct balancer_virtual_services_info *info
);

////////////////////////////////////////////////////////////////////////////////

/// Persistent config-independent info about real
struct balancer_real_info {
	// virtual service ip
	uint8_t vip[16];
	int virtual_ip_proto; // IPPROTO_IPV4 or IPPROTO_IPV6

	// port of the virtual service
	// zero if PURE_L3 flag enabled
	uint16_t virtual_port;

	// real ip
	uint8_t ip[16];
	int real_ip_proto; // IPPROTO_IPV4 or IPPROTO_IPV6

	// virtual service transport protocol
	int transport_proto; // IPPROTO_TCP or IPPROTO_UDP

	// number of active connections
	size_t active_connections;

	// last packet timestamp
	uint32_t last_packet_timestamp;

	// number of created connections so far
	size_t created_connections;

	// number of packets sent to real
	size_t send_packets;

	// number of bytes sent to real
	size_t send_bytes;

	// number of packets which
	// are dropped because real is disabled
	size_t dropped_packets;
};

struct balancer_reals_info {
	size_t count;
	struct balancer_real_info *info;
};

/// Fills real info.
/// @returns -1 on error.
int
balancer_fill_real_info(
	struct balancer_state *state, struct balancer_reals_info *info
);

void
balancer_free_real_info(
	struct balancer_state *state, struct balancer_reals_info *info
);