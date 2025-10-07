#pragma once

#include <stdint.h>
#include <time.h>

////////////////////////////////////////////////////////////////////////////////

struct balancer_clock {
	_Atomic uint32_t current_time;
};

static inline void
clock_init(struct balancer_clock *clock) {
	clock->current_time = 0;
}

static inline int
clock_update_time(struct balancer_clock *clock) {
	uint32_t now = time(NULL);
	if (now != clock->current_time) {
		__c11_atomic_store(&clock->current_time, now, __ATOMIC_SEQ_CST);
		return 1;
	}
	return 0;
}

static inline uint32_t
clock_get_time(struct balancer_clock *clock) {
	return __c11_atomic_load(&clock->current_time, __ATOMIC_SEQ_CST);
}