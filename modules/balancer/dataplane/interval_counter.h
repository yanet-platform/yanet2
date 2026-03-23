#pragma once

#include "common/likely.h"
#include "common/memory_address.h"
#include <assert.h>
#include <stdint.h>
#include <string.h>

struct rt_interval_counter {
	int32_t *diff;
	uint32_t last_timestamp;
};

struct rt_interval_counter_config {
	size_t time_ring_size;
	size_t time_ring_size_mask;
};

static inline int64_t
rt_interval_counter_try_reset(
	struct rt_interval_counter *counter,
	const struct rt_interval_counter_config *config,
	uint32_t now
) {
	int32_t *diff = ADDR_OF(&counter->diff);
	int64_t sum = 0;
	if (unlikely(now - counter->last_timestamp >= config->time_ring_size)) {
		for (size_t i = 0; i < config->time_ring_size; ++i) {
			sum += diff[i];
		}
		memset(diff, 0, config->time_ring_size * sizeof(int32_t));
		counter->last_timestamp = now;
	}
	return sum;
}

static inline int64_t
rt_interval_counter_advance(
	struct rt_interval_counter *counter,
	const struct rt_interval_counter_config *config,
	uint32_t now
) {
	int32_t *diff = ADDR_OF(&counter->diff);
	int64_t change = 0;
	while (unlikely(counter->last_timestamp < now)) {
		uint32_t idx =
			counter->last_timestamp & config->time_ring_size_mask;
		counter->last_timestamp++;
		change += diff[idx];
		diff[idx] = 0;
	}
	uint32_t idx = counter->last_timestamp & config->time_ring_size_mask;
	change += diff[idx];
	diff[idx] = 0;
	return change;
}

static inline int64_t
rt_interval_counter_make(
	struct rt_interval_counter *counter,
	const struct rt_interval_counter_config *config,
	uint32_t now,
	uint32_t len
) {
	assert(len < config->time_ring_size);

	int64_t change = rt_interval_counter_try_reset(counter, config, now);

	int32_t *diff = ADDR_OF(&counter->diff);
	diff[now & config->time_ring_size_mask] += 1;
	diff[(now + len) & config->time_ring_size_mask] -= 1;

	return change + rt_interval_counter_advance(counter, config, now);
}

static inline int64_t
rt_interval_counter_prolong(
	struct rt_interval_counter *counter,
	const struct rt_interval_counter_config *config,
	uint32_t now,
	uint32_t last_right,
	uint32_t new_right
) {
	assert(last_right >= now);
	assert(new_right - now < config->time_ring_size);

	int64_t change = rt_interval_counter_try_reset(counter, config, now);

	int32_t *diff = ADDR_OF(&counter->diff);
	diff[last_right & config->time_ring_size_mask] += 1;
	diff[new_right & config->time_ring_size_mask] -= 1;

	return change + rt_interval_counter_advance(counter, config, now);
}