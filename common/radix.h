#pragma once

#include <stdint.h>
#include <stdlib.h>

#include <string.h>

#define RADIX_VALUE_INVALID 0xffffffff
#define RADIX_CHUNK_SIZE 16

/*
 * RADIX tree maps a n-byte array value into one unsigned one. The tree
 * organized into n-page tree where first n-1 lookups denotes next page and the
 * last one return the stored value.
 *
 * Each page is 256 items-wide with each item is 4-byte unsigned integer.
 * Any uninitialized value is marked with the special flag.
 */

typedef uint32_t radix_page_t[256];

// TODO: chunked storage
struct radix {
	radix_page_t **pages;
	size_t page_count;
};

static inline radix_page_t *
radix_page(const struct radix *radix, uint32_t page_idx) {
	return radix->pages[page_idx / RADIX_CHUNK_SIZE] +
	       page_idx % RADIX_CHUNK_SIZE;
}

static inline int
radix_init(struct radix *radix) {
	radix->pages = (radix_page_t **)malloc(sizeof(radix_page_t *) * 1);
	if (radix->pages == NULL)
		return -1;
	radix->pages[0] = (radix_page_t *)malloc(sizeof(radix_page_t) * 16);
	if (radix->pages[0] == NULL)
		return -1;
	radix->page_count = 1;
	memset(radix_page(radix, 0), 0xff, sizeof(radix_page_t));
	return 0;
}

static inline void
radix_free(struct radix *radix) {
	for (uint32_t chunk_idx = 0;
	     chunk_idx < radix->page_count / RADIX_CHUNK_SIZE;
	     ++chunk_idx) {
		free(radix->pages[chunk_idx]);
	}
	free(radix->pages);
}

static inline uint32_t
radix_new_page(struct radix *radix, uint32_t *page_idx) {
	if (!(radix->page_count % RADIX_CHUNK_SIZE)) {
		uint32_t new_chunk_count =
			radix->page_count / RADIX_CHUNK_SIZE + 1;
		radix_page_t **pages = (radix_page_t **)realloc(
			radix->pages, sizeof(radix_page_t *) * new_chunk_count
		);
		if (pages == NULL) {
			return -1;
		}
		radix->pages = pages;
		radix->pages[new_chunk_count - 1] = (radix_page_t *)malloc(
			sizeof(radix_page_t) * RADIX_CHUNK_SIZE
		);
		if (radix->pages[new_chunk_count - 1] == NULL)
			return -1;
	}
	*page_idx = radix->page_count;
	memset(radix_page(radix, radix->page_count), 0xff, sizeof(radix_page_t)
	);
	++(radix->page_count);
	return 0;
}

static inline int
radix_insert(
	struct radix *radix,
	uint8_t key_size,
	const uint8_t *key,
	uint32_t value
) {
	radix_page_t *page = radix_page(radix, 0);

	for (uint8_t iter = 0; iter < key_size - 1; ++iter) {
		uint32_t *stored_value = (*page) + key[iter];
		if (*stored_value == RADIX_VALUE_INVALID &&
		    radix_new_page(radix, stored_value))
			return -1;
		page = radix_page(radix, *stored_value);
	}

	(*page)[key[key_size - 1]] = value;
	return 0;
}

static inline uint32_t
radix_lookup(const struct radix *radix, uint8_t key_size, const uint8_t *key) {
	uint32_t value;
	// Do three page lookups and then retrieve the value
	radix_page_t *page = radix_page(radix, 0);
	for (uint8_t iter = 0; iter < key_size - 1; ++iter) {
		value = (*page)[key[iter]];
		if (value == RADIX_VALUE_INVALID)
			return RADIX_VALUE_INVALID;

		page = radix_page(radix, value);
	}
	value = (*page)[key[key_size - 1]];
	return value;
}

/*
 * RADIX iterate callback invoked for each valid value. The key is encoded
 * using big-endian.
 */
typedef int (*radix_iterate_func)(
	uint8_t key_size, const uint8_t *key, uint32_t value, void *data
);

/*
 * The routine iterates through whole RADIX and invokes a callback for
 * each valid key/value pair.
 */
static inline int
radix_walk(
	const struct radix *radix,
	uint8_t key_size,
	radix_iterate_func iterate_func,
	void *iterate_func_data
) {
	uint8_t key[key_size];
	radix_page_t *pages[key_size];

	uint8_t depth = 0;
	key[depth] = 0;
	pages[depth] = radix_page(radix, 0);

	while (1) {
		uint32_t value = (*pages[depth])[key[depth]];

		if (value != RADIX_VALUE_INVALID) {
			if (depth == key_size - 1) {
				if (iterate_func(
					    key_size,
					    key,
					    value,
					    iterate_func_data
				    ))
					return -1;
			} else {
				pages[depth + 1] = radix_page(radix, value);
				key[depth + 1] = 0;
				++depth;
				continue;
			}
		}

		key[depth]++;
		if (key[depth] == 0) {
			if (depth == 0)
				break;
			--depth;
			key[depth]++;
		}
	}
	return 0;
}

static inline int
radix64_insert(struct radix *radix64, const uint8_t *key, uint32_t value) {
	return radix_insert(radix64, 8, key, value);
}

static inline uint32_t
radix64_lookup(const struct radix *radix64, const uint8_t *key) {
	return radix_lookup(radix64, 8, key);
}

static inline int
radix64_walk(
	const struct radix *radix64,
	radix_iterate_func iterate_func,
	void *iterate_func_data
) {
	return radix_walk(radix64, 8, iterate_func, iterate_func_data);
}

static inline int
radix32_insert(struct radix *radix32, const uint8_t *key, uint32_t value) {
	return radix_insert(radix32, 4, key, value);
}

static inline uint32_t
radix32_lookup(const struct radix *radix32, const uint8_t *key) {
	return radix_lookup(radix32, 4, key);
}

static inline int
radix32_walk(
	const struct radix *radix32,
	radix_iterate_func iterate_func,
	void *iterate_func_data
) {
	return radix_walk(radix32, 4, iterate_func, iterate_func_data);
}
