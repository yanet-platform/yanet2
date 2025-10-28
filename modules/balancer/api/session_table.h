#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

struct agent;

// Table of the established connections
struct balancer_session_table;

// Allocate new session table
struct balancer_session_table *
balancer_session_table_create(struct agent *agent, size_t size);

// Free session table
void
balancer_session_table_free(struct balancer_session_table *session_table);

// Extend session table if it is small enough
int
balancer_session_table_extend(
	struct balancer_session_table *session_table, bool force
);

// Try free unused memory occupied by session table
int
balancer_session_table_free_unused(struct balancer_session_table *session_table
);
