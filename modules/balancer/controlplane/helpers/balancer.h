#pragma once

#include <stddef.h>
#include <stdint.h>

struct balancer_packet_handler;
struct balancer_session_table;
struct agent;

int
balancer_initial_setup(
	struct agent *agent,
	struct balancer_packet_handler *handler,
	const char *name,
	struct balancer_session_table *session_table
);

int
balancer_register_counters(struct balancer_packet_handler *handler);

const char *
balancer_name(struct balancer_packet_handler *handler);

int
balancer_set_ipv4_vs_matcher(struct balancer_packet_handler *handler);

int
balancer_set_ipv6_vs_matcher(struct balancer_packet_handler *handler);

void
balancer_free_vs_matchers(struct balancer_packet_handler *handler);

int
balancer_set_ipv4_decap_filter(struct balancer_packet_handler *handler);

int
balancer_set_ipv6_decap_filter(struct balancer_packet_handler *handler);

void
balancer_free_decap_filters(struct balancer_packet_handler *handler);