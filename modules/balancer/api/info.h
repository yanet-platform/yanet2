#pragma once

#include <stddef.h>
#include <stdint.h>

#include "modules/balancer/api/stats.h"

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

	// number of active session
	size_t active_sessions;

	// last packet timestamp
	uint32_t last_packet_timestamp;

	// statistics
	struct balancer_vs_stats stats;
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
	size_t active_sessions;

	// last packet timestamp
	uint32_t last_packet_timestamp;

	// statistics
	struct balancer_real_stats stats;
};

struct balancer_reals_info {
	size_t count;
	struct balancer_real_info *info;
};

/// Fills reals info.
/// @returns -1 on error.
int
balancer_fill_reals_info(
	struct balancer_state *state, struct balancer_reals_info *info
);

/// Fills real info.
/// @return -1 if such real not found.
int
balancer_fill_real_info(
	struct balancer_state *state,
	size_t real_idx,
	struct balancer_real_info *info
);

void
balancer_free_reals_info(
	struct balancer_state *state, struct balancer_reals_info *info
);