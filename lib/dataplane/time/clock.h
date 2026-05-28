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
	uint64_t tsc_to_ns;
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

// Signature of the per-round wall-time read.
//
// The default implementation calls tsc_clock_get_time_ns. Tests and
// alternative harnesses may install a substitute by assigning to
// dataplane_time_ns_fn before any worker activity begins. The
// substitute may ignore the clock argument and read a process-global
// mock time source instead.
typedef uint64_t (*dataplane_time_ns_fn_t)(struct tsc_clock *clock);

// Process-global override point for wall-time reads inside the worker
// loop.
//
// Defaults to tsc_clock_get_time_ns. Not thread-safe to swap — install
// once before any worker thread starts.
extern dataplane_time_ns_fn_t dataplane_time_ns_fn;
