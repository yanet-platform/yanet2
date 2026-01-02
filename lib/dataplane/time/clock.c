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

	clock->tsc_to_ns = 256e9 / rte_get_tsc_hz();
	return 0;
}

int
tsc_clock_adjust(struct tsc_clock *clock) {
	return tsc_clock_init(clock);
}
