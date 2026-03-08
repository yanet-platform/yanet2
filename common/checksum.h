
#include <stdint.h>

#include <string.h>

static inline uint16_t
csum_reduce(uint32_t cksum) {
	// FIXME: adjust the code for negative prefixes
	cksum = (cksum >> 16) + (cksum & 0xffff);
	cksum = (cksum >> 16) + (cksum & 0xffff);
	return cksum;
}

static inline uint16_t
csum(const void *data, size_t len) {
	uint32_t result = 0;
	while (len >= 2) {
		uint16_t v;
		memcpy(&v, data, 2);
		result += v;

		data = (const void *)((uintptr_t)data + 2);
		len -= 2;
	}

	if (len) {
		uint16_t v = 0;
		memcpy(&v, data, 1);
		result += v;
	}

	return csum_reduce(result);
}

static inline uint16_t
csum_plus(uint16_t val0, uint16_t val1) {
	uint16_t sum = val0 + val1;

	if (sum < val0) {
		++sum;
	}

	return sum;
}

static inline uint16_t
csum_minus(uint16_t val0, uint16_t val1) {
	uint16_t sum = val0 - val1;

	if (sum > val0) {
		--sum;
	}

	return sum;
}
