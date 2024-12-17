#pragma once

/*
 * Longest Prefix Match (LPM) tree used to map a range of n-byte values into
 * 4-byte unsigned one. The tree organized into variable-length page tree
 * where values marked with the special flag.
 *
 * The tree does not allow to rewrite key-ranges or delete them.
 */

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

#include <string.h>

#include "value.h"

#define LPM_VALUE_INVALID 0xffffffff
#define LPM_VALUE_MASK 0x7fffffff
#define LPM_VALUE_FLAG 0x80000000

#define LPM_CHUNK_SIZE 16

typedef uint32_t lpm_page_t[256];

// TODO chunked storage
struct lpm {
	lpm_page_t **pages;
	size_t page_count;
};

static inline lpm_page_t *
lpm_page(const struct lpm *lpm, uint32_t page_idx) {
	return lpm->pages[page_idx / LPM_CHUNK_SIZE] +
	       page_idx % LPM_CHUNK_SIZE;
}

static inline int
lpm_init(struct lpm *lpm) {
	lpm->pages = (lpm_page_t **)malloc(sizeof(lpm_page_t *) * 1);
	if (lpm->pages == NULL)
		return -1;
	lpm->pages[0] = (lpm_page_t *)malloc(sizeof(lpm_page_t) * 16);
	if (lpm->pages[0] == NULL)
		return -1;
	lpm->page_count = 1;
	memset(lpm_page(lpm, 0), 0xff, sizeof(lpm_page_t));
	return 0;
}

static inline void
lpm_free(struct lpm *lpm) {
	for (size_t chunk_idx = 0; chunk_idx < lpm->page_count / LPM_CHUNK_SIZE;
	     ++chunk_idx)
		free(lpm->pages[chunk_idx]);

	free(lpm->pages);
}

static inline int
lpm_new_page(struct lpm *lpm, uint32_t *page_idx) {
	if (!(lpm->page_count % LPM_CHUNK_SIZE)) {
		uint32_t new_chunk_count = lpm->page_count / LPM_CHUNK_SIZE + 1;
		lpm_page_t **pages = (lpm_page_t **)realloc(
			lpm->pages, sizeof(lpm_page_t *) * new_chunk_count
		);
		if (pages == NULL) {
			return -1;
		}
		lpm->pages = pages;
		lpm->pages[new_chunk_count - 1] = (lpm_page_t *)malloc(
			sizeof(lpm_page_t) * LPM_CHUNK_SIZE
		);
		if (lpm->pages[new_chunk_count - 1])
			return -1;
	}
	*page_idx = lpm->page_count;
	memset(lpm_page(lpm, lpm->page_count), 0xff, sizeof(lpm_page_t));
	++(lpm->page_count);
	return 0;
}

static inline int
lpm_check_range_lo(
	uint8_t key_size, const uint8_t *key, const uint8_t *from, uint8_t hop
) {
	uint8_t check[key_size];
	memcpy(check, key, hop + 1);

	memset(check + hop + 1, 0x00, key_size - hop - 1);
	if (memcmp(check, from, key_size) < 0)
		return -1;

	return 0;
}

static inline int
lpm_check_range_hi(
	uint8_t key_size, const uint8_t *key, const uint8_t *to, uint8_t hop
) {
	uint8_t check[key_size];
	memcpy(check, key, hop + 1);

	memset(check + hop + 1, 0xff, key_size - hop - 1);
	if (memcmp(check, to, key_size) > 0)
		return -1;

	return 0;
}

/*
 * The routine maps range [from..to] to value value.
 * Keys are big-endian encoded.
 */
static inline int
lpm_insert(
	struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
) {
	uint8_t key[key_size];
	lpm_page_t *pages[key_size];

	int8_t hop = 0;
	key[hop] = from[hop];
	pages[hop] = lpm_page(lpm, 0);
	int8_t max_hop = 0;

	while (1) {
		uint32_t *stored_value = (*pages[hop]) + key[hop];
		if (*stored_value == LPM_VALUE_INVALID) {
			if (hop < key_size - 1 &&
			    (lpm_check_range_lo(key_size, key, from, hop) ||
			     lpm_check_range_hi(key_size, key, to, hop))) {
				if (lpm_new_page(lpm, stored_value))
					return -1;
				++hop;
				if (hop > max_hop) {
					key[hop] = from[hop];
					max_hop = hop;
				} else {
					key[hop] = 0;
				}
				pages[hop] = lpm_page(lpm, *stored_value);
				continue;
			} else {
				*stored_value = value | LPM_VALUE_FLAG;
			}
		} else if (*stored_value & LPM_VALUE_FLAG) {
			// TODO
		} else {
			++hop;
			if (hop > max_hop) {
				key[hop] = from[hop];
				max_hop = hop;
			} else {
				key[hop] = 0;
			}
			pages[hop] = lpm_page(lpm, *stored_value);
			continue;
		}

		do {
			key[hop]++;
			uint8_t upper_bound = 0xff;
			if (lpm_check_range_hi(key_size, key, to, hop))
				upper_bound = to[hop];
			if (key[hop] == (uint8_t)(upper_bound + 1)) {
				if (hop == 0)
					return 0;
				--hop;
			} else
				break;
		} while (1);
	}

	return 0;
}

static inline uint32_t
lpm_lookup(const struct lpm *lpm, uint8_t key_size, const uint8_t *key) {
	uint32_t value = 0;

	for (uint8_t hop = 0; hop < key_size; ++hop) {
		lpm_page_t *page = lpm_page(lpm, value);
		value = (*page)[key[hop]];
		if (value == LPM_VALUE_INVALID)
			return value;
		if (value & LPM_VALUE_FLAG)
			return value & LPM_VALUE_MASK;
	}

	return LPM_VALUE_INVALID;
}

typedef int (*lpm_walk_func)(
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value,
	void *data
);

static inline int
lpm_walk(
	const struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	lpm_walk_func walk_func,
	void *walk_func_data
) {
	uint8_t key[key_size];
	memset(key, 0, key_size);
	lpm_page_t *pages[key_size];

	int8_t hop = 0;
	key[hop] = from[hop];
	pages[hop] = lpm_page(lpm, 0);
	int8_t max_hop = 0;

	uint32_t prev_value = LPM_VALUE_INVALID;
	uint8_t prev_from[key_size];
	memcpy(prev_from, from, key_size);
	uint8_t prev_to[key_size];

	while (1) {
		uint32_t value = (*pages[hop])[key[hop]];
		if (value == LPM_VALUE_INVALID) {
			// TODO: handle unintialized value
		} else if (value & LPM_VALUE_FLAG) {
			if (prev_value != value) {
				if (prev_value != LPM_VALUE_INVALID) {
					if (walk_func(
						    key_size,
						    prev_from,
						    prev_to,
						    prev_value & LPM_VALUE_MASK,
						    walk_func_data
					    )) {
						return -1;
					}
				}

				prev_value = value;
				memcpy(prev_from, key, key_size);
				memset(prev_from + hop + 1,
				       0x00,
				       key_size - hop - 1);
			}
			memcpy(prev_to, key, key_size);
			memset(prev_to + hop + 1, 0xff, key_size - hop - 1);
		} else {
			++hop;
			if (hop > max_hop) {
				key[hop] = from[hop];
				max_hop = hop;
			} else {
				key[hop] = 0;
			}
			pages[hop] = lpm_page(lpm, value);
			continue;
		}

		do {
			key[hop]++;
			uint8_t upper_bound = 0xff;
			if (lpm_check_range_hi(key_size, key, to, hop))
				upper_bound = to[hop];
			if (key[hop] == (uint8_t)(upper_bound + 1)) {
				if (hop == 0)
					goto out;
				--hop;
			} else
				break;
		} while (1);
	}

out:

	if (prev_value != LPM_VALUE_INVALID) {
		if (walk_func(
			    key_size,
			    prev_from,
			    prev_to,
			    prev_value & LPM_VALUE_MASK,
			    walk_func_data
		    )) {
			return -1;
		}
	}

	return 0;
}

/*
 * LPM iteration callback called for each valid value.
 */
typedef int (*lpm_collect_values_func)(uint32_t value, void *data);

/*
 * Collect all valid values for [from..to] key range.
 */
static inline int
lpm_collect_values(
	const struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	lpm_collect_values_func collect_func,
	void *collect_func_data
) {
	uint8_t key[key_size];
	lpm_page_t *pages[key_size];

	int8_t hop = 0;
	key[hop] = from[hop];
	pages[hop] = lpm_page(lpm, 0);

	while (1) {
		uint32_t value = (*pages[hop])[key[hop]];
		if (value == LPM_VALUE_INVALID) {
			// TODO: handle unintialized value: should we call cb?
		} else if (value & LPM_VALUE_FLAG) {
			if (collect_func(
				    value & LPM_VALUE_MASK, collect_func_data
			    )) {
				return -1;
			}
		} else {
			++hop;
			key[hop] = from[hop];
			pages[hop] = lpm_page(lpm, value);
			continue;
		}

		do {
			key[hop]++;
			uint8_t upper_bound = 0xff;
			if (lpm_check_range_hi(key_size, key, to, hop))
				upper_bound = to[hop];
			if (key[hop] == (uint8_t)(upper_bound + 1)) {
				if (hop == 0)
					goto out;
				--hop;
			} else
				break;
		} while (1);
	}

out:

	return 0;
}

/*
 * The routine combine LPM and value table mapping.
 * This means that some value from the LPM could map into one value from
 * the mapping. Compactification assumes rewrite LPM stored values into mapped
 * ones.
 */
static inline void
lpm_remap(struct lpm *lpm, uint8_t key_size, struct value_table *table) {
	uint8_t key[key_size];
	lpm_page_t *pages[key_size];

	int8_t hop = 0;
	key[hop] = 0;
	pages[hop] = lpm_page(lpm, 0);

	while (1) {
		uint32_t value = (*pages[hop])[key[hop]];
		if (value == LPM_VALUE_INVALID) {

		} else if (value & LPM_VALUE_FLAG) {
			(*pages[hop])[key[hop]] =
				value_table_get(
					table, 0, value & LPM_VALUE_MASK
				) |
				LPM_VALUE_FLAG;
		} else {
			++hop;
			key[hop] = 0;
			pages[hop] = lpm_page(lpm, value);
			continue;
		}

		do {
			key[hop]++;
			if (key[hop] == 0) {
				if (hop == 0)
					goto out;
				--hop;
			} else
				break;
		} while (1);
	}

out:
	return;
}

static inline void
lpm_compact(struct lpm *lpm, uint8_t key_size) {
	uint8_t key[key_size];
	lpm_page_t *pages[key_size];

	int8_t hop = 0;
	key[hop] = 0;
	pages[hop] = lpm_page(lpm, 0);

	while (1) {
		uint32_t value = (*pages[hop])[key[hop]];
		if (value == LPM_VALUE_INVALID || value & LPM_VALUE_FLAG) {
		} else {
			++hop;
			key[hop] = 0;
			pages[hop] = lpm_page(lpm, value);
			continue;
		}

		do {
			key[hop]++;
			if (key[hop] == 0) {
				if (hop == 0)
					goto out;

				bool is_monolite = 1;
				uint32_t first_value = (*pages[hop])[0];
				for (uint8_t idx = 255; idx > 0; --idx)
					is_monolite &= first_value ==
						       (*pages[hop])[idx];

				--hop;
				if (is_monolite &&
				    (first_value & LPM_VALUE_FLAG)) {
					(*pages[hop])[key[hop]] = first_value;
				}
			} else {
				break;
			}
		} while (1);
	}

out:
	return;
}

static inline int
lpm8_insert(
	struct lpm *lpm8, const uint8_t *from, const uint8_t *to, uint32_t value
) {
	return lpm_insert(lpm8, 8, from, to, value);
}

static inline uint32_t
lpm8_lookup(const struct lpm *lpm8, const uint8_t *key) {
	return lpm_lookup(lpm8, 8, key);
}

static inline int
lpm8_collect_values(
	const struct lpm *lpm8,
	const uint8_t *from,
	const uint8_t *to,
	lpm_collect_values_func collect_func,
	void *collect_func_data
) {
	return lpm_collect_values(
		lpm8, 8, from, to, collect_func, collect_func_data
	);
}

static inline int
lpm8_walk(
	const struct lpm *lpm8,
	const uint8_t *from,
	const uint8_t *to,
	lpm_walk_func walk_func,
	void *walk_func_data
) {
	return lpm_walk(lpm8, 8, from, to, walk_func, walk_func_data);
}

static inline void
lpm8_remap(struct lpm *lpm8, struct value_table *table) {
	return lpm_remap(lpm8, 8, table);
}

static inline void
lpm8_compact(struct lpm *lpm8) {
	return lpm_compact(lpm8, 8);
}

static inline int
lpm4_insert(
	struct lpm *lpm4, const uint8_t *from, const uint8_t *to, uint32_t value
) {
	return lpm_insert(lpm4, 4, from, to, value);
}

static inline uint32_t
lpm4_lookup(const struct lpm *lpm4, const uint8_t *key) {
	return lpm_lookup(lpm4, 4, key);
}

static inline int
lpm4_collect_values(
	const struct lpm *lpm4,
	const uint8_t *from,
	const uint8_t *to,
	lpm_collect_values_func collect_func,
	void *collect_func_data
) {
	return lpm_collect_values(
		lpm4, 4, from, to, collect_func, collect_func_data
	);
}

static inline int
lpm4_walk(
	const struct lpm *lpm4,
	const uint8_t *from,
	const uint8_t *to,
	lpm_walk_func walk_func,
	void *walk_func_data
) {
	return lpm_walk(lpm4, 4, from, to, walk_func, walk_func_data);
}

static inline void
lpm4_remap(struct lpm *lpm4, struct value_table *table) {
	return lpm_remap(lpm4, 4, table);
}

static inline void
lpm4_compact(struct lpm *lpm4) {
	return lpm_compact(lpm4, 4);
}
