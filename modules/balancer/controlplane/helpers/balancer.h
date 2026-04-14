/*
 * Balancer packet handler helpers.
 *
 * Error convention: functions returning int use 0 for success and
 * non-zero for failure. Specific codes:
 *   -1  shared memory allocation failure
 *   -2  heap allocation failure
 */
#pragma once

#include <stddef.h>
#include <stdint.h>

struct balancer_packet_handler;
struct balancer_session_table;
struct agent;

/* Zero-initializes handler, sets up cp_module and session table pointer.
 * Returns 0 on success, -1 on failure. */
int
balancer_initial_setup(
	struct agent *agent,
	struct balancer_packet_handler *handler,
	const char *name,
	struct balancer_session_table *session_table
);

/* Registers per-VS, per-real, and module-level counters.
 * Returns 0 on success, -1 on failure. */
int
balancer_register_counters(struct balancer_packet_handler *handler);

const char *
balancer_name(struct balancer_packet_handler *handler);

/* Compile and set the IPv4/IPv6 VS matcher filter.
 * Returns 0 on success, -1 on shared memory allocation failure,
 * -2 on heap allocation failure. */
int
balancer_set_ipv4_vs_matcher(struct balancer_packet_handler *handler);

int
balancer_set_ipv6_vs_matcher(struct balancer_packet_handler *handler);

void
balancer_free_vs_matchers(struct balancer_packet_handler *handler);

/* Compile and set the IPv4/IPv6 decap address filter.
 * Returns 0 on success, -1 on shared memory allocation failure,
 * -2 on heap allocation failure. */
int
balancer_set_ipv4_decap_filter(struct balancer_packet_handler *handler);

int
balancer_set_ipv6_decap_filter(struct balancer_packet_handler *handler);

void
balancer_free_decap_filters(struct balancer_packet_handler *handler);