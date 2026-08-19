#pragma once

#include "common/likely.h"
#include <stdint.h>

/// TODO: docs
static inline uint64_t
uint64_log_up(uint64_t value) {
	if (unlikely(value == 0)) {
		return 0;
	}

	return sizeof(long long) * 8 - __builtin_clzll(value) -
	       !(value & (value - 1));
}

/// TODO: docs
static inline uint64_t
uint64_log_down(uint64_t value) {
	if (unlikely(value == 0)) {
		return 0;
	}

	return sizeof(long long) * 8 - 1 - __builtin_clzll(value);
}

// next_power_of_two returns the smallest power of two not less than the input.
//
// Zero maps to one. Values above 2^63 saturate at 2^63 because the next
// power would overflow the return type.
static inline uint64_t
next_power_of_two(uint64_t value) {
	if (value <= 1) {
		return 1;
	}
	if (value > (1ull << 63)) {
		return 1ull << 63;
	}
	return 1ull << (64 - __builtin_clzll(value - 1));
}

/**
 * @brief Finds the next number divisible by the provided power of 2
 * @param n Input number
 * @param pow2 Divisor, must be power of 2 (pow2 = 2^k) for some k.
 * @return The smallest `x` such that `x` >= `n` and `x` % `pow2` == 0
 */
static inline uint64_t
next_divisible_pow2(uint64_t n, uint64_t pow2) {
	return (n + (pow2 - 1)) & ~(pow2 - 1);
}

#define ALIGN_DOWN_POW2(x) (1UL << (63 - __builtin_clzl(x)))
