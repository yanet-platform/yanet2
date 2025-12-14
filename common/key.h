#pragma once

#include <endian.h>
#include <stdint.h>
#include <string.h>

static inline void
filter_key_inc(uint8_t key_size, uint8_t *key) {
	for (int16_t idx = key_size - 1; idx >= 0; --idx) {
		if (key[idx]++ != 0xff)
			break;
	}
}

static inline void
filter_key_dec(uint8_t key_size, uint8_t *key) {
	for (int16_t idx = key_size - 1; idx >= 0; --idx) {
		if (key[idx]-- != 0x00)
			break;
	}
}

static inline void
filter_key_apply_prefix(
	uint8_t key_size, const uint8_t *from, uint8_t *to, uint8_t prefix
) {
	memcpy(to, from, key_size);
	if (prefix % 8)
		to[prefix / 8] |= ((uint16_t)1 << (8 - prefix % 8)) - 1;
	for (uint8_t idx = (prefix + 7) / 8; idx < key_size; ++idx)
		to[idx] |= 0xff;
}

static inline int
filter_key_cmp(uint8_t key_size, const uint8_t *l, const uint8_t *r) {
	for (size_t i = 0; i < key_size; ++i) {
		if (l[i] < r[i])
			return -1;
		if (l[i] > r[i])
			return 1;
	}
	return 0;
}
