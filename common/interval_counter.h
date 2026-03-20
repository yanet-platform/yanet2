#pragma once

#include <stddef.h>

#include "common/memory.h"
#include "rcu.h"

////////////////////////////////////////////////////////////////////////////////

#define INTERVAL_COUNTER_WORKERS RCU_WORKERS

struct interval_counter {
	struct memory_context mctx;
	
	const size_t range_size; // == 1 << range_size_exp
	const size_t range_size_exp;

	uint64_t *updates[RCU_WORKERS]; // aligned by range_size
};