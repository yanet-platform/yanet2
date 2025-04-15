#pragma once

#include <stdint.h>

static inline uint64_t
uint64_log(uint64_t value) {
	if (value == 0)
		return 0;

	return sizeof(long long) * 8 - __builtin_clzll(value) -
	       !(value & (value - 1));
}
