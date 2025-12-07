#pragma once

#include <common/network.h>

#include <stddef.h>
#include <stdint.h>

struct agent;
struct balancer_vs_config;
struct balancer_state;

////////////////////////////////////////////////////////////////////////////////

/// Timeouts of different types of sessions created
// by balancer.
struct balancer_sessions_timeouts {
	// Timeouts of sessions, which are
	// created or updated with TCP SYN ACK packets.
	uint32_t tcp_syn_ack;

	// Timeouts of sessions, which are
	// create or updated with TCP SYN packets.
	uint32_t tcp_syn;

	// Timeouts of sessions,
	// which are update with TCP FIN packets.
	uint32_t tcp_fin;

	// Default timeout for TCP packets.
	uint32_t tcp;

	// Default timeout for UDP packets.
	uint32_t udp;

	// Default timeout for packets,
	// which does not match the enumerated
	// categories.
	uint32_t def;
};

////////////////////////////////////////////////////////////////////////////////

/// Creates new config for the balancer module.
/// @param agent Balancer agent.
/// @param name Name of the module config.
/// @param session_table Table of the connections between clients and real
/// servers.
/// @param vs_count Number of the virtual services.
/// @param vs_configs List of vs_count pointers to virtual-service configs.
/// @param sessions_timeouts Session timeouts configuration.
/// @return Pointer to the module configuration instance on success; NULL of
/// failure.
struct cp_module *
balancer_module_config_create(
	struct agent *agent,
	const char *name,
	struct balancer_state *state,
	struct balancer_sessions_timeouts *sessions_timeouts,
	size_t vs_count,
	struct balancer_vs_config **vs_configs,
	struct net4_addr *source_addr,
	struct net6_addr *source_addr_v6,
	size_t decap_addr_count,
	struct net4_addr *decap_addrs,
	size_t decap_addr_v6_count,
	struct net6_addr *decap_addrs_v6
);

/// Frees module memory if it is not used in dataplane.
/// @param cp_module Previously configured module.
void
balancer_module_config_free(struct cp_module *cp_module);
