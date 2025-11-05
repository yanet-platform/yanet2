#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

struct agent;

/// Table of the established connections.
/// One table can be used in the different configurations of the balancer
/// module.
struct balancer_session_table;

/// Create new session table.
/// @param agent Agent which memory context will be used to allocate table.
/// @param size Number of sessions for which memory will be reserved.
struct balancer_session_table *
balancer_session_table_create(struct agent *agent, size_t size);

/// Free session table.
void
balancer_session_table_free(struct balancer_session_table *session_table);

/// Extend session table x2 if table is filled enough.
/// @param session_table Session Table.
/// @param force Force extension unconditionally.
int
balancer_session_table_extend(
	struct balancer_session_table *session_table, bool force
);

/// Free unused memory occupied by session table.
int
balancer_session_table_free_unused(struct balancer_session_table *session_table
);
