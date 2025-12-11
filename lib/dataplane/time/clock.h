#pragma once

#include <stdint.h>

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
	// Real time when clock was init in nanoseconds.
	uint64_t real_time_ns;

	// Timestamp counter when clock was init.
	uint64_t timestamp_counter;
};

// Initialize clock.
int
tsc_clock_init(struct tsc_clock *clock);

// Adjust clock (calls init under the hood).
int
tsc_clock_adjust(struct tsc_clock *clock);

// Get current real time in nanoseconds.
uint64_t
tsc_clock_get_time_ns(struct tsc_clock *clock);
