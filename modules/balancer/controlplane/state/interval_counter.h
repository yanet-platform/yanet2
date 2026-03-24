#pragma once

/**
 * @file rt_interval_counter.h
 *
 * Control-plane initialization helpers for [`struct
 * rt_interval_counter`](modules/balancer/dataplane/interval_counter.h:19).
 */

#include "modules/balancer/dataplane/interval_counter.h"

/* Initialize a counter so it can start tracking intervals at `now`. */
void
rt_interval_counter_init(struct rt_interval_counter *counter, uint32_t now);
