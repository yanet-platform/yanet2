#include "clock.h"

#include <rte_cycles.h>

#include <time.h>

////////////////////////////////////////////////////////////////////////////////

int
tsc_clock_init(struct tsc_clock *clock) {
	clock->timestamp_counter = rte_rdtsc();
	return clock_gettime(CLOCK_REALTIME, &clock->real_time);
}

int
tsc_clock_adjust(struct tsc_clock *clock) {
	return tsc_clock_init(clock);
}

struct timespec
tsc_clock_get_time(struct tsc_clock *clock) {
	const uint64_t k1e9 = 1000 * 1000 * 1000;

	uint64_t tsc = rte_rdtsc();

	// todo: inline it somewhere,
	// may be in build_config.h
	uint64_t tsc_hz = rte_get_tsc_hz();

	uint64_t tsc_delta = tsc - clock->timestamp_counter;

	uint64_t ns_now =
		(tsc_delta * k1e9) / tsc_hz + clock->real_time.tv_nsec;

	return (struct timespec){
		.tv_sec = clock->real_time.tv_sec + ns_now / k1e9,
		.tv_nsec = ns_now % k1e9,
	};
}