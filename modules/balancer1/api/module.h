#pragma once

#include <stddef.h>
#include <stdint.h>

struct agent;
struct balancer_session_table;
struct balancer_vs_config;

// Create new config for the balancer module
struct cp_module *
balancer_module_config_create(
	struct agent *agent,
	const char *name,
	struct balancer_session_table *session_table,
	size_t vs_count,
	struct balancer_vs_config **vs_configs,
	uint32_t tcp_syn_ack_timeout,
	uint32_t tcp_syn_timeout,
	uint32_t tcp_fin_timeout,
	uint32_t tcp_timeout,
	uint32_t udp_timeout,
	uint32_t default_timeout
);

// Free balancer module config
void
balancer_module_config_free(struct cp_module *cp_module);
