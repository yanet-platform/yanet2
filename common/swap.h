#pragma once

#include <assert.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

static inline void
swap(void *left, void *right, size_t size) {
	uint8_t tmp[size];
	memcpy(tmp, left, size);
	memcpy(left, right, size);
	memcpy(right, tmp, size);
}

#define SWAP(left, right)                                                      \
	do {                                                                   \
		assert(sizeof(*(left)) == sizeof(*(right)));                   \
		swap((void *)(left), (void *)(right), sizeof(*(left)));        \
	} while (0)
