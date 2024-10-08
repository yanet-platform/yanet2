#ifndef LPM_H
#define LPM_H

/*
 * Longest Prefix Match (LPM) tree used to map a range of 8-byte values into
 * 4-byte unsigned one. The tree organized into variable-length page tree
 * where values marked with the special flag.
 *
 * The tree does not allow to reassign key-ranges or delete them.
 */

#include <stdint.h>
#include <stdlib.h>
#include <stdbool.h>

#include <string.h>

#include "value.h"

#define LPM_VALUE_INVALID 0xffffffff
#define LPM_VALUE_MASK 0x7fffffff
#define LPM_VALUE_FLAG 0x80000000
typedef uint32_t lpm64_page_t[256];

//TODO chunked storage
struct lpm64 {
	lpm64_page_t **pages;
	size_t page_count;
};

static inline lpm64_page_t *
lpm64_page(const struct lpm64 *lpm64, uint32_t page_idx)
{
	return lpm64->pages[page_idx / 16] + page_idx % 16;
}

static inline int
lpm64_init(struct lpm64 *lpm64)
{
	lpm64->pages = (lpm64_page_t **)malloc(sizeof(lpm64_page_t *) * 1);
	if (lpm64->pages == NULL)
		return -1;
	lpm64->pages[0] = (lpm64_page_t *)malloc(sizeof(lpm64_page_t) * 16);
	if (lpm64->pages[0] == NULL)
		return -1;
	lpm64->page_count = 1;
	memset(lpm64_page(lpm64, 0), 0xff, sizeof(lpm64_page_t));
	return 0;
}

static inline int
lpm64_new_page(struct lpm64 *lpm64, uint32_t *page_idx)
{
	if (!(lpm64->page_count % 16)) {
		uint32_t new_chunk_count = lpm64->page_count / 16 + 1;
		lpm64_page_t **pages =
			(lpm64_page_t **)realloc(lpm64->pages,
						 sizeof(lpm64_page_t *) *
						 new_chunk_count);
		if (pages == NULL) {
			return -1;
		}
		lpm64->pages = pages;
		lpm64->pages[new_chunk_count - 1] =
			(lpm64_page_t *)malloc(sizeof(lpm64_page_t) * 16);
		if (lpm64->pages[new_chunk_count - 1])
			return -1;
	}
	*page_idx = lpm64->page_count;
	memset(lpm64_page(lpm64, lpm64->page_count), 0xff, sizeof(lpm64_page_t));
	++(lpm64->page_count);
	return 0;
}

/*
 * The routine maps range [from..to] to value value.
 * Keys are big-endian encoded.
 */
static inline int
lpm64_insert(struct lpm64 *lpm64, uint64_t from, uint64_t to, uint32_t value)
{
	uint8_t *from_bytes = (uint8_t *)&from;
	uint8_t *to_bytes = (uint8_t *)&to;

	lpm64_page_t *page = lpm64_page(lpm64, 0);
	uint8_t hop = 0;
	do {
		if (from_bytes[hop] != to_bytes[hop])
			break;

		// go down - use existing page or allocate a new one
		uint32_t *stored_value = (*page) + from_bytes[hop];
		if (*stored_value == LPM_VALUE_INVALID &&
		    lpm64_new_page(lpm64, stored_value))
			return -1;
		page = lpm64_page(lpm64, *stored_value);
	} while (++hop < 7);

	for (uint16_t idx = from_bytes[hop]; idx <= to_bytes[hop]; ++idx)
		(*page)[idx] = value | LPM_VALUE_FLAG;
	return 0;
}

static inline uint32_t
lpm64_lookup(const struct lpm64 *lpm64, uint64_t key)
{
	uint8_t *key_bytes = (uint8_t *)&key;

	uint32_t value;

	for (uint8_t hop = 0; hop < 8; ++hop) {
		lpm64_page_t *page = lpm64_page(lpm64, value);
		value = (*page)[key_bytes[hop]];
		if (value == LPM_VALUE_INVALID)
			return value;
		if (value & LPM_VALUE_FLAG)
			return value & LPM_VALUE_MASK;
	}

	return LPM_VALUE_INVALID;
}

/*
 * LPM iteration callback called for each valid value. Key is big-endian
 * encoded.
 */
typedef void (*lpm64_iterate_func)(
	uint64_t key,
	uint32_t value,
	void *data
);

/*
 * Collect all valid values for [from..to] key range. Keys are big-endian.
 * The routine does not invoke callback if the previous one value is equal the
 * next one so the function return only valid values but not key:value pairs.
 */
static inline void
lpm64_walk(
	const struct lpm64 *lpm,
	uint64_t from, uint64_t to,
	lpm64_iterate_func iterate_func,
	void *iterate_func_data)
{
	uint8_t *from_bytes = (uint8_t *)&from;
	uint8_t *to_bytes = (uint8_t *)&to;

	uint8_t keys[8];
	lpm64_page_t *pages[8];

	int8_t hop = 0;
	keys[hop] = from_bytes[hop];
	pages[hop] = lpm64_page(lpm, 0);
	uint32_t prev_value = LPM_VALUE_INVALID;

	while (1) {
		uint32_t value = (*pages[hop])[keys[hop]];
		if (value == LPM_VALUE_INVALID) {


		} else if (value & LPM_VALUE_FLAG) {
			if (value != prev_value) {
				iterate_func(
					*(uint64_t *)keys,
					value & LPM_VALUE_MASK,
					iterate_func_data);
				prev_value = value;
			}
		} else {
			++hop;
			keys[hop] = from_bytes[hop];
			pages[hop] = lpm64_page(lpm, value);
			continue;
		}

		keys[hop]++;
		if (keys[hop] == (uint8_t)(to_bytes[hop] + 1)) {
			if (hop == 0)
				break;
			--hop;
			keys[hop]++;
			if (keys[0] == (uint8_t)(to_bytes[hop] + 1))
				break;
		}
	}
}

/*
 * The routine combine LPM and value table mapping.
 * This means that some value from the LPM could map into one value from
 * the mapping. Compactification assumes rewrite LPM stored values into mapped
 * ones and make LPM as small as possible.
 */
static inline void
lpm64_compact(
	struct lpm64 *lpm,
	struct value_table *table)
{
	uint8_t keys[8];
	lpm64_page_t *pages[8];

	int8_t hop = 0;
	keys[hop] = 0;
	pages[hop] = lpm64_page(lpm, 0);

	while (1) {
		uint32_t value = (*pages[hop])[keys[hop]];
		if (value == LPM_VALUE_INVALID) {


		} else if (value & LPM_VALUE_FLAG) {
			(*pages[hop])[keys[hop]] = value_table_get(
				table,
				0,
				value & LPM_VALUE_MASK) | LPM_VALUE_FLAG;
		} else {
			++hop;
			keys[hop] = 0;
			pages[hop] = lpm64_page(lpm, value);
			continue;
		}

		keys[hop]++;
		if (keys[hop] == 0) {
			if (hop == 0)
				break;
			/*
			 * The code bellow squash page if there is only
			 * one value set decreasing the tree branch length.
			 */
			bool is_monolite = 1;
			uint32_t first_value = (*pages[hop])[0];
			for (uint8_t idx = 255; idx > 0; --idx)
				is_monolite &= first_value == (*pages[hop])[idx];

			--hop;
			if (is_monolite && (first_value & LPM_VALUE_FLAG)) {
				(*pages[hop])[keys[hop]] = first_value;
			}

			keys[hop]++;
			if (keys[0] == 0)
				break;
		}
	}
}

#endif
