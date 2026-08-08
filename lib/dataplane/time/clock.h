#pragma once

#include <stdint.h>

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
	// Tick-to-nanosecond multiplier in 32.32 fixed-point.
	//
	// Nanoseconds per tick, scaled by 2^32. Convert a tick count with
	// tsc_clock_ticks_to_ns.
	uint64_t tsc_to_ns_mult;
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

// Build a 32.32 fixed-point tick-to-nanosecond multiplier from a TSC
// frequency in Hz.
//
// Returns 0 when hz is 0 instead of dividing by it, keeping the helper
// a total function that never traps — an integer divide by zero would
// raise SIGFPE — and preserving the old code's frozen-clock behaviour
// rather than crashing. Callers that must reject an uncalibrated TSC
// check hz themselves, which is what tsc_clock_init does, because only
// it has an error channel to report on.
static inline uint64_t
tsc_clock_mult_from_hz(uint64_t hz) {
	if (hz == 0) {
		return 0;
	}

	return ((1ULL << 32) * 1000000000ULL) / hz;
}

// Convert a tick count to nanoseconds using a 32.32 fixed-point
// tick-to-nanosecond multiplier.
//
// The multiplication is carried out in 128 bits so that it does not
// overflow uint64_t before the final shift.
static inline uint64_t
tsc_clock_ticks_to_ns(uint64_t ticks, uint64_t mult) {
	return (uint64_t)(((__uint128_t)ticks * mult) >> 32);
}
