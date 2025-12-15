#pragma once

#include <stdint.h>
#include <stdlib.h>

#include <assert.h>

#include <string.h>

#include "memory.h"
#include "memory_address.h"

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

struct radix {
	struct memory_context *memory_context;
	radix_page_t **pages;
	uint64_t page_count;
};

static inline radix_page_t *
radix_page(const struct radix *radix, uint32_t page_idx) {
	radix_page_t **pages = ADDR_OF(&radix->pages);
	radix_page_t *chunk = ADDR_OF(&pages[page_idx / RADIX_CHUNK_SIZE]);
	return chunk + page_idx % RADIX_CHUNK_SIZE;
}

static inline int
radix_new_page(struct radix *radix, uint32_t *page_idx) {
	if (!(radix->page_count % RADIX_CHUNK_SIZE)) {
		struct memory_context *memory_context =
			ADDR_OF(&radix->memory_context);

		radix_page_t *new_chunk = memory_balloc(
			memory_context, sizeof(radix_page_t) * RADIX_CHUNK_SIZE
		);

		if (new_chunk == NULL)
			return -1;

		radix_page_t **old_pages = ADDR_OF(&radix->pages);
		uint64_t old_chunk_count = radix->page_count / RADIX_CHUNK_SIZE;
		uint64_t new_chunk_count = old_chunk_count + 1;
		radix_page_t **new_pages = (radix_page_t **)memory_balloc(
			memory_context, new_chunk_count * sizeof(*new_pages)
		);
		if (new_pages == NULL) {
			memory_bfree(
				memory_context,
				new_chunk,
				sizeof(radix_page_t) * RADIX_CHUNK_SIZE
			);
			return -1;
		}

		// Set correct relative addresses
		for (size_t i = 0; i < old_chunk_count; ++i) {
			EQUATE_OFFSET(&new_pages[i], &old_pages[i]);
		}

		SET_OFFSET_OF(&new_pages[new_chunk_count - 1], new_chunk);
		SET_OFFSET_OF(&radix->pages, new_pages);

		memory_bfree(
			memory_context,
			old_pages,
			old_chunk_count * sizeof(radix_page_t *)
		);
	}
	if (page_idx != NULL)
		*page_idx = radix->page_count;
	radix_page_t *page = radix_page(radix, radix->page_count);
	memset(page, 0xff, sizeof(radix_page_t));
	radix->page_count += 1;
	return 0;
}

static inline int
radix_init(struct radix *radix, struct memory_context *memory_context) {
	SET_OFFSET_OF(&radix->memory_context, memory_context);
	radix->pages = NULL;
	radix->page_count = 0;
	return radix_new_page(radix, NULL);
}

static inline void
radix_free(struct radix *radix) {
	struct memory_context *memory_context = ADDR_OF(&radix->memory_context);
	radix_page_t **pages = ADDR_OF(&radix->pages);
	uint32_t chunk_count =
		(radix->page_count + RADIX_CHUNK_SIZE - 1) / RADIX_CHUNK_SIZE;
	for (uint32_t chunk_idx = 0; chunk_idx < chunk_count; ++chunk_idx) {
		radix_page_t *chunk = ADDR_OF(&pages[chunk_idx]);
		if (chunk == NULL)
			continue;

		memory_bfree(
			memory_context,
			chunk,
			sizeof(radix_page_t) * RADIX_CHUNK_SIZE
		);
		SET_OFFSET_OF(pages + chunk_idx, NULL);
	}
	memory_bfree(
		memory_context, pages, sizeof(radix_page_t *) * chunk_count
	);
	SET_OFFSET_OF(&radix->pages, NULL);
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
		if ((*stored_value == RADIX_VALUE_INVALID) &&
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

static inline void
radix_walk_rec(
	const struct radix *radix,
	uint8_t key_size,
	uint8_t *key,
	radix_page_t *page,
	uint8_t depth,
	radix_iterate_func cb,
	void *cb_data
) {
	for (uint16_t next = 0; next < 256; ++next) {
		uint32_t value = (*page)[next];
		if (value == RADIX_VALUE_INVALID) {
			continue;
		}
		key[depth] = next;
		if (depth + 1 < key_size) {
			radix_walk_rec(
				radix,
				key_size,
				key,
				radix_page(radix, value),
				depth + 1,
				cb,
				cb_data
			);
		} else {
			cb(key_size, key, value, cb_data);
		}
	}
}

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
	radix_walk_rec(
		radix,
		key_size,
		key,
		radix_page(radix, 0),
		0,
		iterate_func,
		iterate_func_data
	);
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
