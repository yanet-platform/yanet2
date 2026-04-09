#pragma once

#include <stddef.h>
#include <stdint.h>

struct agent;
struct balancer_packet_handler;
struct balancer_session_table;

int
balancer_agent_install(
	struct agent *agent, struct balancer_packet_handler *handler
);

int
balancer_agent_register(
	struct agent *agent, struct balancer_packet_handler *handler
);

void
balancer_agent_forget(
	struct agent *agent, struct balancer_packet_handler *handler
);

struct balancer_packet_handler **
balancer_agent_list(struct agent *agent, size_t *count);

struct balancer_session_table *
balancer_agent_create_st(struct agent *agent, size_t capacity);

void
balancer_agent_destroy_st(
	struct agent *agent, struct balancer_session_table *st
);