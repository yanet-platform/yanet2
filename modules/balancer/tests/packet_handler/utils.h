#pragma once

#include "config.h"
#include <stddef.h>

#include <common/memory.h>

////////////////////////////////////////////////////////////////////////////////

struct cp_module *make_balancer(struct memory_context *mctx, size_t workers, struct balancer_state_config *cfg);