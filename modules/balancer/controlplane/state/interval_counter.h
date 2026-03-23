#pragma once

/**
 * @file rt_interval_counter.h
 *
 * Allocation and deallocation for rt_interval_counter instances.
 */

#include "modules/balancer/dataplane/interval_counter.h"

void
rt_interval_counter_init(struct rt_interval_counter *counter, uint32_t now);
