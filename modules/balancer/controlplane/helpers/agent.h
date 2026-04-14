/*
 * Agent-level helpers: handler installation/registration, handler
 * storage management, and session table lifecycle.
 *
 * Error convention: functions returning int use 0 for success
 * and -1 for failure (allocation or storage error).
 */
#pragma once

#include <stddef.h>
#include <stdint.h>

struct agent;
struct balancer_packet_handler;
struct balancer_session_table;

/* Push handler's cp_module into the dataplane module list.
 * Returns 0 on success, -1 on failure. */
int
balancer_agent_install(
	struct agent *agent, struct balancer_packet_handler *handler
);

/* Register handler in the per-agent balancer_storage array (reuses
 * NULL slots before growing). Returns 0 on success, -1 on failure. */
int
balancer_agent_register(
	struct agent *agent, struct balancer_packet_handler *handler
);

/* Remove handler from balancer_storage by NULLing its slot. */
void
balancer_agent_forget(
	struct agent *agent, struct balancer_packet_handler *handler
);

/* Return the array of registered handlers and set *count.
 * Returns NULL and leaves *count unchanged when no storage exists. */
struct balancer_packet_handler **
balancer_agent_list(struct agent *agent, size_t *count);

/* Allocate and initialize a session table with the given capacity.
 * Returns NULL on allocation failure. */
struct balancer_session_table *
balancer_agent_create_st(struct agent *agent, size_t capacity);

void
balancer_agent_destroy_st(
	struct agent *agent, struct balancer_session_table *st
);