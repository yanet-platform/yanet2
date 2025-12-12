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
	// todo: inline it somewhere,
	// may be in build_config.h
	const uint64_t tsc_hz = rte_get_tsc_hz();

	uint64_t tsc = rte_rdtsc();

	uint64_t tsc_delta = tsc - clock->timestamp_counter;

	// Split into whole seconds and fractional part
	uint64_t whole_seconds = tsc_delta / tsc_hz;
	uint64_t remaining_cycles = tsc_delta % tsc_hz;

	// Convert to nanoseconds: seconds * 1e9 + (remaining_cycles * 1e9 /
	// tsc_hz)
	uint64_t ns_delta =
		whole_seconds * k1e9 + (remaining_cycles * k1e9) / tsc_hz;

	uint64_t ns_now = ns_delta + clock->real_time_ns;

	return ns_now;
}