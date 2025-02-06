
#include <stdint.h>

inline uint16_t
csum_plus(uint16_t val0, uint16_t val1) {
	uint16_t sum = val0 + val1;

	if (sum < val0) {
		++sum;
	}

	return sum;
}

inline uint16_t
csum_minus(uint16_t val0, uint16_t val1) {
	uint16_t sum = val0 - val1;

	if (sum > val0) {
		--sum;
	}

	return sum;
}
