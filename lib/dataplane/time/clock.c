#include "clock.h"

#include <time.h>

#include <rte_cycles.h>

int
tsc_clock_init(struct tsc_clock *clock) {
	struct timespec ts;
	if (clock_gettime(CLOCK_REALTIME, &ts)) {
		return -1;
	}

	uint64_t hz = rte_get_tsc_hz();
	if (hz == 0) {
		return -1;
	}

	clock->real_time_ns = ts.tv_nsec + ts.tv_sec * 1000000000ULL;
	clock->timestamp_counter = rte_rdtsc();

	clock->tsc_to_ns_mult = tsc_clock_mult_from_hz(hz);

	clock->real_time_ns -= tsc_clock_ticks_to_ns(
		clock->timestamp_counter, clock->tsc_to_ns_mult
	);

	return 0;
}

int
tsc_clock_adjust(struct tsc_clock *clock) {
	return tsc_clock_init(clock);
}

uint64_t
tsc_clock_get_time_ns(struct tsc_clock *clock) {
	uint64_t tsc = rte_rdtsc();

	return clock->real_time_ns +
	       tsc_clock_ticks_to_ns(tsc, clock->tsc_to_ns_mult);
}

dataplane_time_ns_fn_t dataplane_time_ns_fn = tsc_clock_get_time_ns;
