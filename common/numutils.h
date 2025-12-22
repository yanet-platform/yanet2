#pragma once

#include <stdint.h>

static inline uint64_t
uint64_log(uint64_t value) {
	if (value == 0)
		return 0;

	return sizeof(long long) * 8 - __builtin_clzll(value) -
	       !(value & (value - 1));
}

/**
 * @brief Align number up to next power of 2
 * @param n Input number
 * @return Next power of 2, or 0 if overflow
 */
static inline uint64_t
align_up_pow2(uint64_t x) {
	// Hacker's delight 2nd Chapter 3. Power-of-2 Boundaries
	// Rounding Up
	x--;
	x |= x >> 1;
	x |= x >> 2;
	x |= x >> 4;
	x |= x >> 8;
	x |= x >> 16;
	x |= x >> 32;
	return x + 1;
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
