#pragma once

#include <stddef.h>
#include <stdint.h>

struct agent;
struct balancer_session_table;
struct balancer_vs_config;

// Allocate new config for the balancer module
struct cp_module *
balancer_module_config_create(
	struct agent *agent,
	const char *name,
	struct balancer_session_table *sessions
);

// Free balancer module config
void
balancer_module_config_free(struct cp_module *cp_module);

// Set timeouts for records of different kinds in the session table
void
balancer_module_config_set_timeouts(
	struct cp_module *module,
	uint32_t tcp_syn_ack_timeout,
	uint32_t tcp_syn_timeout,
	uint32_t tcp_fin_timeout,
	uint32_t tcp_timeout,
	uint32_t udp_timeout,
	uint32_t default_timeout
);

// Add virtual services
int
balancer_module_config_add_vs(
	struct cp_module *module, struct balancer_vs_config **vs, size_t count
);
