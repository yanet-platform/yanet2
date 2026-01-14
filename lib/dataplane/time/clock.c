#include "clock.h"

#include <time.h>

#include <rte_cycles.h>
#include <x86intrin.h>

////////////////////////////////////////////////////////////////////////////////

int
tsc_clock_init(struct tsc_clock *clock) {
	struct timespec ts;
	if (clock_gettime(CLOCK_REALTIME, &ts)) {
		return -1;
	}
	clock->real_time_ns = ts.tv_nsec + ts.tv_sec * 1e9;
	clock->timestamp_counter = rte_rdtsc();

	clock->tsc_to_ns = 1024e9 / rte_get_tsc_hz();

	clock->real_time_ns -=
		clock->timestamp_counter * clock->tsc_to_ns >> 10;

	return 0;
}

int
tsc_clock_adjust(struct tsc_clock *clock) {
	return tsc_clock_init(clock);
}

uint64_t
tsc_clock_get_time_ns(struct tsc_clock *clock) {
	uint64_t tsc = _rdtsc();

	return clock->real_time_ns + (tsc >> 10) * clock->tsc_to_ns;
}
