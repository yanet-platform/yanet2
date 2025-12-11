#include "clock.h"

#include <rte_cycles.h>

#include <time.h>

////////////////////////////////////////////////////////////////////////////////

static const uint64_t k1e9 = 1000 * 1000 * 1000;

int
tsc_clock_init(struct tsc_clock *clock) {
	clock->timestamp_counter = rte_rdtsc();
	struct timespec ts;
	if (clock_gettime(CLOCK_REALTIME, &ts)) {
		return -1;
	}
	clock->real_time_ns = ts.tv_nsec + ts.tv_sec * k1e9;
	return 0;
}

int
tsc_clock_adjust(struct tsc_clock *clock) {
	return tsc_clock_init(clock);
}

uint64_t
tsc_clock_get_time_ns(struct tsc_clock *clock) {
	uint64_t tsc = rte_rdtsc();

	// todo: inline it somewhere,
	// may be in build_config.h
	const uint64_t tsc_hz = rte_get_tsc_hz();

	uint64_t tsc_delta = tsc - clock->timestamp_counter;

	uint64_t ns_now = (tsc_delta * k1e9) / tsc_hz + clock->real_time_ns;

	return ns_now;
}