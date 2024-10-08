#ifndef RADIX_H
#define RADIX_H

/*
 * RADIX tree maps 8-byte values into one unsigned one. The tree organized
 * into 8-level page tree whre first 7 lookups denotes next page and the last
 * one return the stored value.
 *
 * Each page is 256 items-wide with each item is 4-byte unsigned integer.
 * Any uninitialized value is marked with the special flag.
 */

#include <stdint.h>
#include <stdlib.h>

#include <string.h>

#define RADIX_VALUE_INVALID 0xffffffff
typedef uint32_t radix64_page_t[256];

//TODO: chunked storage
struct radix64 {
	radix64_page_t **pages;
	size_t page_count;
};

static inline radix64_page_t *
radix64_page(const struct radix64 *radix64, uint32_t page_idx)
{
	return radix64->pages[page_idx / 16] + page_idx % 16;
}

static int
radix64_init(struct radix64 *radix64)
{
	radix64->pages = (radix64_page_t **)malloc(sizeof(radix64_page_t *) * 1);
	if (radix64->pages == NULL)
		return -1;
	radix64->pages[0] = (radix64_page_t *)malloc(sizeof(radix64_page_t) * 16);
	if (radix64->pages[0] == NULL)
		return -1;
	radix64->page_count = 1;
	memset(radix64_page(radix64, 0), 0xff, sizeof(radix64_page_t));
	return 0;
}

static uint32_t
radix64_new_page(struct radix64 *radix64, uint32_t *page_idx)
{
	if (!(radix64->page_count % 16)) {
		uint32_t new_chunk_count = radix64->page_count / 16 + 1;
		radix64_page_t **pages =
			(radix64_page_t **)realloc(radix64->pages,
						   sizeof(radix64_page_t *) *
					           new_chunk_count);
		if (pages == NULL) {
			return -1;
		}
		radix64->pages = pages;
		radix64->pages[new_chunk_count - 1] =
			(radix64_page_t *)malloc(sizeof(radix64_page_t) * 16);
		if (radix64->pages[new_chunk_count - 1] == NULL)
			return -1;
	}
	*page_idx = radix64->page_count;
	memset(radix64_page(radix64, radix64->page_count),
		0xff,
		sizeof(radix64_page_t));
	++(radix64->page_count);
	return 0;
}

static int
radix64_insert(struct radix64 *radix64, uint64_t key, uint32_t value)
{
	const uint8_t *key_bytes = (uint8_t *)&key;
	radix64_page_t *page = radix64_page(radix64, 0);

	for (uint32_t iter = 0; iter < 7; ++iter) {
		uint32_t *stored_value = (*page) + key_bytes[iter];
		if (*stored_value == RADIX_VALUE_INVALID &&
		    radix64_new_page(radix64, stored_value))
			return -1;
		page = radix64_page(radix64, *stored_value);
	}

	(*page)[key_bytes[7]] = value;
	return 0;
}

static uint32_t
radix64_lookup(const struct radix64 *radix64, uint64_t key)
{
	const uint8_t *key_bytes = (uint8_t *)&key;
	uint32_t value;
	// Do three page lookups and then retrieve the value
	radix64_page_t *page = radix64_page(radix64, 0);
	for (uint32_t iter = 0; iter < 7; ++iter) {
		value = (*page)[key_bytes[iter]];
		if (value == RADIX_VALUE_INVALID)
			return RADIX_VALUE_INVALID;

		page = radix64_page(radix64, value);
	}
	value = (*page)[key_bytes[7]];
	return value;
}

/*
 * RADIX iterate callback invoked for each valid value. The key is encoded
 * using key bytes using big-endian.
 */
typedef void (*radix64_iterate_func)(
	uint64_t key,
	uint32_t value,
	void *data
);

/*
 * The routine iterates through whole RADIX and invokes a callback for
 * each valid key/value pair.
 */
static void
radix64_iterate(
	const struct radix64 *radix64,
	radix64_iterate_func iterate_func,
	void *iterate_func_data)
{
	uint8_t keys[8];
	radix64_page_t *pages[8];

	uint8_t depth = 0;
	keys[depth] = 0;
	pages[depth] = radix64_page(radix64, 0);

	while (1) {
		uint32_t value = (*pages[depth])[keys[depth]];

		if (value != RADIX_VALUE_INVALID) {
			if (depth == 7) {
				uint64_t key = *(uint64_t *)keys;
				iterate_func(key, value, iterate_func_data);
			} else {
				pages[depth + 1] = radix64_page(radix64, value);
				keys[depth + 1] = 0;
				++depth;
				continue;
			}
		}

		keys[depth]++;
		if (keys[depth] == 0) {
			if (depth == 0)
				break;
			--depth;
			keys[depth]++;
		}
	}
}

#endif
