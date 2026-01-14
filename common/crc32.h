#pragma once

#include <stdint.h>
#include <string.h>

static inline uint32_t
crc32_u8(uint8_t v, uint32_t hash) {
	return __builtin_ia32_crc32qi(hash, v);
}

static inline uint32_t
crc32_u16(uint16_t v, uint32_t hash) {
	return __builtin_ia32_crc32hi(hash, v);
}

static inline uint32_t
crc32_u32(uint32_t v, uint32_t hash) {
	return __builtin_ia32_crc32si(hash, v);
}

static inline uint32_t
crc32_u64(uint64_t v, uint32_t hash) {
	return __builtin_ia32_crc32di(hash, v);
}

static inline uint32_t
crc32(const void *data, uint64_t size, uint32_t hash) {

	for (uint64_t idx = 0; idx < size / 8; ++idx) {
		uint64_t value;
		memcpy(&value, data, 8);
		hash = crc32_u64(value, hash);
		data += 8;
	}

	if (size & 0x4) {
		uint32_t value;
		memcpy(&value, data, 4);
		hash = crc32_u32(value, hash);
		data += 4;
	}

	if (size & 0x2) {
		uint16_t value;
		memcpy(&value, data, 2);
		hash = crc32_u16(value, hash);
		data += 2;
	}

	if (size & 0x1)
		hash = crc32_u8(*(const uint8_t *)data, hash);

	return hash;
}
