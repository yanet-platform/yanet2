#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/network.h"

#include "stats.h"

////////////////////////////////////////////////////////////////////////////////

/// Info about balancer state.

// Balancer state
struct balancer_state;

////////////////////////////////////////////////////////////////////////////////

/// Persistent config-independent info about virtual service
struct balancer_virtual_service_info {
	// ip
	uint8_t ip[NET6_LEN];
	int ip_proto; // IPPROTO_IPV4 or IPPROTO_IPV6

	// port of the virtual service
	// zero if PURE_L3 flag enabled
	uint16_t virtual_port;

	// virtual service transport protocol
	int transport_proto; // IPPROTO_TCP or IPPROTO_UDP

	// last packet timestamp
	uint32_t last_packet_timestamp;

	// statistics
	struct balancer_vs_stats stats;
};

struct balancer_virtual_services_info {
	size_t count;
	struct balancer_virtual_service_info *info;
};

/// Fills virtual services info.
/// @returns -1 on error.
int
balancer_fill_virtual_services_info(
	struct balancer_state *state,
	struct balancer_virtual_services_info *info
);

/// Fills virtual service info.
/// @returns -1 on error.
int
balancer_fill_virtual_service_info(
	struct balancer_state *state,
	size_t virtual_service_idx,
	struct balancer_virtual_service_info *info
);

void
balancer_free_virtual_services_info(
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

////////////////////////////////////////////////////////////////////////////////

// Info about balancer state.
struct balancer_info {
	// Statistics of the balancer.
	struct balancer_stats stats;

	// Info about virtual services.
	struct balancer_virtual_services_info virtual_services;

	// Info about real services.
	struct balancer_reals_info reals;
};

int
balancer_fill_info(struct balancer_state *state, struct balancer_info *info);

void
balancer_free_info(struct balancer_state *state, struct balancer_info *info);

////////////////////////////////////////////////////////////////////////////////

// Info about balancer session between
// client and reals server.
struct balancer_session_info {
	uint32_t vs_id;

	uint8_t client_ip[16];
	uint16_t client_port;

	uint32_t real_id;
	uint32_t create_timestamp;
	uint32_t last_packet_timestamp;
	uint32_t timeout;
};

// Info about balancer sessions with
// possible duplicates.
struct balancer_sessions_info {
	size_t count;
	struct balancer_session_info *sessions;
};

// Fill info about active sessions with possible duplicates.
int
balancer_fill_sessions_info(
	struct balancer_state *state,
	struct balancer_sessions_info *info,
	uint32_t now,
	bool count_only
);

// Free info about active sessions.
void
balancer_free_sessions_info(
	struct balancer_state *state, struct balancer_sessions_info *info
);