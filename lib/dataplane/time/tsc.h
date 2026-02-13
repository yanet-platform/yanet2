#pragma once

#include "rte_cycles.h"
#include <stdint.h>

#define TSC_SHIFT 32

////////////////////////////////////////////////////////////////////////////////

static inline uint64_t
tsc_timestamp_ns() {
	static uint64_t tsc_mult = ~0ULL;

	// One-time initialization
	if (unlikely(tsc_mult == ~0ULL)) {
		uint64_t hz = rte_get_tsc_hz();
		if (unlikely(hz == 0)) {
			return 0;
		}

		// Verify we won't overflow during multiplication
		// Max safe TSC value: ~18 years at 5GHz, which should be fine
		tsc_mult = ((1ULL << TSC_SHIFT) * 1000000000ULL) / hz;
	}

	uint64_t current_tsc = rte_rdtsc();

// Check if your compiler/platform supports __uint128_t
#ifdef __SIZEOF_INT128__
	uint64_t timestamp_ns =
		((__uint128_t)current_tsc * tsc_mult) >> TSC_SHIFT;
#else
	// Fallback for platforms without 128-bit support
	uint64_t high = (current_tsc >> 32) * tsc_mult;
	uint64_t low = (current_tsc & 0xFFFFFFFF) * tsc_mult;
	uint64_t timestamp_ns = (high >> (TSC_SHIFT - 32)) + (low >> TSC_SHIFT);
#endif
	return timestamp_ns;
}

static inline uint64_t
tsc_elapsed_ns(uint64_t elapsed_tsc) {
	static uint64_t tsc_mult = ~0ULL;

	// One-time initialization
	if (unlikely(tsc_mult == ~0ULL)) {
		uint64_t hz = rte_get_tsc_hz();
		if (unlikely(hz == 0)) {
			return 0;
		}

		// Verify we won't overflow during multiplication
		// Max safe TSC value: ~18 years at 5GHz, which should be fine
		tsc_mult = ((1ULL << TSC_SHIFT) * 1000000000ULL) / hz;
	}

// Check if your compiler/platform supports __uint128_t
#ifdef __SIZEOF_INT128__
	uint64_t elapsed_ns =
		((__uint128_t)elapsed_tsc * tsc_mult) >> TSC_SHIFT;
#else
	// Fallback for platforms without 128-bit support
	uint64_t high = (elapsed_tsc >> 32) * tsc_mult;
	uint64_t low = (elapsed_tsc & 0xFFFFFFFF) * tsc_mult;
	uint64_t elapsed_ns = (high >> (TSC_SHIFT - 32)) + (low >> TSC_SHIFT);
#endif
	return elapsed_ns;
}

#undef TSC_SHIFT