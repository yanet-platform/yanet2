#pragma once

#include "common/memory.h"
#include "modules/balancer/dataplane/interval_counter.h"

int
rt_interval_counter_init(
	struct rt_interval_counter *counter,
	const struct rt_interval_counter_config *config,
	uint32_t now,
	struct memory_context *mctx
);

void
rt_interval_counter_free(
	struct rt_interval_counter *counter,
	const struct rt_interval_counter_config *config,
	struct memory_context *mctx
);