#pragma once

#include <stdint.h>
#include <time.h>
#include <stdatomic.h>

////////////////////////////////////////////////////////////////////////////////

struct balancer_clock {
	_Atomic uint32_t current_time;
};

static inline void
clock_init(struct balancer_clock *clock) {
	clock->current_time = time(NULL);
}

static inline int
clock_update_time(struct balancer_clock *clock) {
	uint32_t now = time(NULL);
	if (now != clock->current_time) {
		atomic_store(&clock->current_time, now);
		return 1;
	}
	return 0;
}

static inline uint32_t
clock_get_time(struct balancer_clock *clock) {
	return atomic_load(&clock->current_time);
}