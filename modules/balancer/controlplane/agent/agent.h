#pragma once

#include <stddef.h>
#include <stdint.h>

#include "modules/balancer/controlplane/api/balancer.h"

struct balancer_agent;

struct balancer_agent_adjust_weights_config {
	size_t adjust_power;
	size_t max_real_weight;
};

struct balancer_agent_balancer_config {
	char balancer_name[80];
	struct balancer_config balancer_config;

	struct balancer_agent_adjust_weights_config adjust_weights_config;

	uint32_t refresh_period; // in ms

	size_t adjust_weights_vs_count;
	uint32_t adjust_weights_vs[];
};

struct balancer_agent_balancer {
	struct balancer_handle *handle;
	struct balancer_agent_balancer_config config;
};

struct yanet_shm;

struct balancer_agent *
balancer_agent(struct yanet_shm *shm, size_t memory);

struct balancer_agent_balancers {
	size_t count;
	struct balancer_agent_balancer *balancers;
};

void
balancer_agent_balancers(
	struct balancer_agent *agent, struct balancer_agent_balancers *balancers
);

void
balancer_agent_balancers_free(struct balancer_agent_balancers *balancers);

int
balancer_agent_update_balancer(
	struct balancer_agent *agent,
	struct balancer_agent_balancer_config *config,
	struct balancer_agent_balancer *balancer
);

const char *
balancer_agent_take_error_msg(struct balancer_agent *agent);