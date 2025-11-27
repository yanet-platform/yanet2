#pragma once

#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

static inline uint64_t
wyhash64(uint64_t wyhash64_x) {
	wyhash64_x += 0x60bee2bee120fc15;
	__uint128_t tmp;
	tmp = (__uint128_t)wyhash64_x * 0xa3b195354a39b70d;
	uint64_t m1 = (tmp >> 64) ^ tmp;
	tmp = (__uint128_t)m1 * 0x1b03738712fad5c9;
	uint64_t m2 = (tmp >> 64) ^ tmp;
	return m2;
}