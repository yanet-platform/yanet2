#pragma once

#include <stddef.h>

#include <common/memory.h>
#include <common/network.h>

#include "controlplane.h"
#include "session.h"
#include "state.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_state *
make_balancer_state(
	struct memory_context *mctx, size_t workers, size_t reserve
);

////////////////////////////////////////////////////////////////////////////////

struct cp_module *
make_balancer(
	struct memory_context *mctx,
	struct balancer_session_timeouts *timeouts,
	struct balancer_state *state
);

////////////////////////////////////////////////////////////////////////////////