#include "time.h"

<<<<<<< HEAD
#include <stdint.h>
=======
>>>>>>> a8887a2 (feat: dataplane worker clock)
#include <threads.h>

thread_local struct timespec current_time = {0, 0};

<<<<<<< HEAD
=======
void
set_current_time(struct timespec *ts) {
	current_time = *ts;
}

>>>>>>> a8887a2 (feat: dataplane worker clock)
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

<<<<<<< HEAD
uint64_t
tsc_clock_get_time_ns(struct tsc_clock *clock) {
	(void)clock;
	return current_time.tv_sec * (uint64_t)1000 * 1000 * 1000 +
	       current_time.tv_nsec;
=======
struct timespec
tsc_clock_get_time(struct tsc_clock *clock) {
	(void)clock;
	return current_time;
>>>>>>> a8887a2 (feat: dataplane worker clock)
}