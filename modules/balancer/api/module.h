#pragma once

#include <stddef.h>
#include <stdint.h>

struct agent;
struct balancer_vs_config;
struct balancer_state;

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
	size_t vs_count,
	struct balancer_vs_config **vs_configs
);

/// Frees module memory if it is not used in dataplane.
/// @param cp_module Previously configured module.
void
balancer_module_config_free(struct cp_module *cp_module);
