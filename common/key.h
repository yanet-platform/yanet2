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
	to[prefix / 8] |= ((uint16_t)1 << (8 - prefix % 8)) - 1;
	for (uint8_t idx = (prefix + 7) / 8; idx < key_size; ++idx)
		to[idx] |= 0xff;
}

static inline int
filter_key_cmp(uint8_t key_size, const uint8_t *l, const uint8_t *r) {
	while (key_size >= 8) {
		uint64_t lv = be64toh(*(uint64_t *)l);
		uint64_t rv = be64toh(*(uint64_t *)r);
		if (lv < rv)
			return -1;
		if (lv > rv)
			return 1;

		l += 8;
		r += 8;
		key_size -= 8;
	}

	if (key_size >= 4) {
		uint32_t lv = be32toh(*(uint32_t *)l);
		uint32_t rv = be32toh(*(uint32_t *)r);
		if (lv < rv)
			return -1;
		if (lv > rv)
			return 1;

		l += 4;
		r += 4;
		key_size -= 4;
	}

	if (key_size >= 2) {
		uint16_t lv = be16toh(*(uint16_t *)l);
		uint16_t rv = be16toh(*(uint16_t *)r);
		if (lv < rv)
			return -1;
		if (lv > rv)
			return 1;

		l += 2;
		r += 2;
		key_size -= 2;
	}

	if (key_size > 0) {
		if (*l < *r)
			return -1;
		if (*l > *r)
			return 1;
	}

	return 0;
}
