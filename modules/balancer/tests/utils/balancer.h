#pragma once

#include <stddef.h>
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

struct memory_context;
struct balancer_state;
struct balancer_session_timeouts;

struct balancer_state *
make_balancer_state(
	struct memory_context *mctx, size_t workers, size_t reserve
);

void
set_current_time(struct balancer_state *state, uint32_t value);

////////////////////////////////////////////////////////////////////////////////

struct cp_module *
make_balancer(
	struct memory_context *mctx,
	struct balancer_session_timeouts *timeouts,
	struct balancer_state *state
);

////////////////////////////////////////////////////////////////////////////////