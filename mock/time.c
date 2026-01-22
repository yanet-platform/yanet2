#include "time.h"

#include <stdint.h>
#include <threads.h>
#include <time.h>

#include "common/spinlock.h"

struct time {
	struct spinlock lock;
	struct timespec ts;
};

static struct time current_time = {.lock = {.locked = false}, .ts = {0, 0}};

void
set_current_time(struct timespec *ts) {
	spinlock_lock(&current_time.lock);
	current_time.ts = *ts;
	spinlock_unlock(&current_time.lock);
}

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

uint64_t
tsc_clock_get_time_ns(struct tsc_clock *clock) {
	(void)clock;
	spinlock_lock(&current_time.lock);
	uint64_t res = current_time.ts.tv_sec * (uint64_t)1000 * 1000 * 1000 +
		       current_time.ts.tv_nsec;
	spinlock_unlock(&current_time.lock);
	return res;
}