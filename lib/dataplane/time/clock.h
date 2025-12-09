#pragma once

#include <stdint.h>
#include <time.h>

////////////////////////////////////////////////////////////////////////////////

// Represents clock, which can be used to get
// current real time.
//
// In dataplane, we need fast real time,
// but we can not use rdtsc() (not real time),
// or clock_gettime (slow). So, we store
// some real time point and TSC, corresponding to it.
// To get current real time, we use current TSC and TSC HZ
// (which is constant on the modern CPUs).
//
// Note: Such scheme can introduce clock drift.
// if we adjust real time at least once
// in a day, there will be no more than 80ms
// clock drift on TSC with 1ppm drift
// (modern CPUs have drift of 0.1-1 ppm).
struct tsc_clock {
	// Real time when clock was init.
	struct timespec real_time;

	// Timestamp counter when clock was init.
	uint64_t timestamp_counter;
};

// Initialize clock.
int
tsc_clock_init(struct tsc_clock *clock);

// Adjust clock (calls init under the hood).
int
tsc_clock_adjust(struct tsc_clock *clock);

// Get current real time.
struct timespec
tsc_clock_get_time(struct tsc_clock *clock);
