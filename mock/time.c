#include "time.h"

#include <threads.h>

thread_local struct timespec current_time = {0, 0};

// Mock tsc clock

struct tsc_clock;

int
tsc_clock_init(struct tsc_clock *clock) {
	(void)clock;
	return 0;
}

int
tsc_clock_adjust(struct tsc_clock *clock) {
	(void)clock;
	return 0;
}

struct timespec
tsc_clock_get_time(struct tsc_clock *clock) {
	(void)clock;
	return current_time;
}