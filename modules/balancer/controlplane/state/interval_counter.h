#pragma once

/**
 * @file rt_interval_counter.h
 *
 * Allocation and deallocation for rt_interval_counter instances.
 */

#include "modules/balancer/dataplane/interval_counter.h"

/**
 * Initialise an interval counter, allocating the ring buffer.
 *
 * The ring is zeroed and last_timestamp is set to `now` so that the
 * first call to make/prolong does not trigger a spurious reset or a
 * long sweep loop.
 *
 * @return 0 on success, -1 if memory allocation fails.
 */
int
rt_interval_counter_init(struct rt_interval_counter *counter, uint32_t now);
