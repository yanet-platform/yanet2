#pragma once

/*
 * Longest Prefix Match (LPM) tree used to map a range of n-byte values into
 * 4-byte unsigned one. The tree organized into variable-length page tree
 * where values marked with the special flag.
 *
 * The tree does not allow to rewrite key-ranges or delete them.
 */

#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

#include <string.h>

#include "key.h"
#include "memory.h"
#include "memory_address.h"
#include "value.h"

#define LPM_VALUE_INVALID 0xffffffff
#define LPM_VALUE_FLAG 0x00000001
#define LPM_VALUE_SET(value) ((value << 1) | LPM_VALUE_FLAG)
#define LPM_VALUE_GET(value) (value >> 1)
#define LPM_CHUNK_SIZE 16

struct lpm_page;

union lpm_value {
	struct lpm_page *page;
	uint64_t value;
};

struct lpm_page {
	union lpm_value values[256];
};

// TODO chunked storage
struct lpm {
	struct memory_context *memory_context;
	struct lpm_page **pages;
	size_t page_count;
};

static inline struct lpm_page *
lpm_page(const struct lpm *lpm, uint32_t page_idx) {
	struct lpm_page **pages = ADDR_OF(&lpm->pages);
	struct lpm_page *chunk = ADDR_OF(&pages[page_idx / LPM_CHUNK_SIZE]);
	return chunk + page_idx % LPM_CHUNK_SIZE;
}

static inline int
lpm_new_page(struct lpm *lpm, union lpm_value *value) {
	if (!(lpm->page_count % LPM_CHUNK_SIZE)) {
		uint32_t old_chunk_count = lpm->page_count / LPM_CHUNK_SIZE;
		uint32_t new_chunk_count = old_chunk_count + 1;

		struct memory_context *memory_context =
			ADDR_OF(&lpm->memory_context);

		struct lpm_page **pages = (struct lpm_page **)memory_balloc(
			memory_context,
			sizeof(struct lpm_page *) * new_chunk_count
		);
		if (pages == NULL) {
			errno = ENOMEM;
			return -1;
		}

		struct lpm_page *page = (struct lpm_page *)memory_balloc(
			memory_context, sizeof(struct lpm_page) * LPM_CHUNK_SIZE
		);
		if (page == NULL) {
			memory_bfree(
				memory_context,
				pages,
				sizeof(struct lpm_page *) * new_chunk_count
			);

			errno = ENOMEM;
			return -1;
		}

		// Set correct relative addresses
		struct lpm_page **old_pages = ADDR_OF(&lpm->pages);
		for (uint64_t chunk_idx = 0; chunk_idx < old_chunk_count;
		     ++chunk_idx) {
			EQUATE_OFFSET(&pages[chunk_idx], &old_pages[chunk_idx]);
		}

		SET_OFFSET_OF(&pages[old_chunk_count], page);
		SET_OFFSET_OF(&lpm->pages, pages);

		memory_bfree(
			memory_context,
			old_pages,
			old_chunk_count * sizeof(struct lpm_page *)
		);
	}
	memset(lpm_page(lpm, lpm->page_count), 0xff, sizeof(struct lpm_page));
	lpm->page_count += 1;

	if (value != NULL)
		SET_OFFSET_OF(&value->page, lpm_page(lpm, lpm->page_count - 1));

	return 0;
}

static inline int
lpm_init(struct lpm *lpm, struct memory_context *memory_context) {
	SET_OFFSET_OF(&lpm->memory_context, memory_context);
	lpm->pages = NULL;
	lpm->page_count = 0;
	return lpm_new_page(lpm, NULL);
}

static inline void
lpm_free(struct lpm *lpm) {
	struct memory_context *memory_context = ADDR_OF(&lpm->memory_context);
	struct lpm_page **pages = ADDR_OF(&lpm->pages);
	if (pages == NULL) {
		return;
	}

	uint32_t chunk_count =
		(lpm->page_count + LPM_CHUNK_SIZE - 1) / LPM_CHUNK_SIZE;

	for (size_t chunk_idx = 0; chunk_idx < chunk_count; ++chunk_idx) {
		memory_bfree(
			ADDR_OF(&lpm->memory_context),
			ADDR_OF(&pages[chunk_idx]),
			sizeof(struct lpm_page) * LPM_CHUNK_SIZE
		);
	}

	memory_bfree(
		memory_context, pages, sizeof(struct lpm_page *) * chunk_count
	);
}

static inline int
lpm_check_range_lo(
	uint8_t key_size, const uint8_t *key, const uint8_t *from, uint8_t hop
) {
	uint8_t check[key_size];
	memcpy(check, key, hop + 1);

	if (hop + 1 < key_size)
		memset(check + hop + 1, 0x00, key_size - hop - 1);
	if (filter_key_cmp(key_size, check, from) < 0)
		return -1;

	return 0;
}

static inline int
lpm_check_range_hi(
	uint8_t key_size, const uint8_t *key, const uint8_t *to, uint8_t hop
) {
	uint8_t check[key_size];
	memcpy(check, key, key_size);

	if (hop + 1 < key_size)
		memset(check + hop + 1, 0xff, key_size - hop - 1);
	if (filter_key_cmp(key_size, check, to) > 0)
		return -1;

	return 0;
}

/**
 * Maps the range [from..to] to a given value.
 *
 * @param from   The starting point of the range (inclusive).
 * @param to     The ending point of the range (inclusive).
 * @param value  The value to which the range is mapped.
 *
 * @note Keys are big-endian encoded.
 *
 * @return On success, returns 0. If an error occurs, sets errno and returns -1.
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
	struct lpm_page *pages[key_size];

	int8_t hop = 0;
	key[hop] = from[hop];
	pages[hop] = lpm_page(lpm, 0);
	int8_t max_hop = 0;

	while (1) {
		union lpm_value *stored_value = pages[hop]->values + key[hop];
		if (stored_value->value & LPM_VALUE_FLAG) {
			/*
			 * Assume there is only invalid values, in the opposite
			 * one have to propagate the value deeper.
			 */
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
				pages[hop] = ADDR_OF(&stored_value->page);
				continue;
			} else {
				stored_value->value = LPM_VALUE_SET(value);
			}
		} else {
			++hop;
			if (hop > max_hop) {
				key[hop] = from[hop];
				max_hop = hop;
			} else {
				key[hop] = 0;
			}
			pages[hop] = ADDR_OF(&stored_value->page);
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
	union lpm_value *value = NULL;
	struct lpm_page *page = lpm_page(lpm, 0);

	for (uint8_t hop = 0; hop < key_size; ++hop) {
		value = page->values + key[hop];
		if (value->value & LPM_VALUE_FLAG)
			break;
		page = ADDR_OF(&value->page);
	}

	return LPM_VALUE_GET(value->value);
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
	struct lpm_page *pages[key_size];

	int8_t hop = 0;
	int8_t hi_limit = 0;

	key[hop] = from[hop];
	pages[hop] = lpm_page(lpm, 0);
	int8_t max_hop = 0;

	uint32_t prev_value = LPM_VALUE_INVALID;
	uint8_t prev_from[key_size];
	memcpy(prev_from, from, key_size);
	uint8_t prev_to[key_size];

	while (1) {
		union lpm_value *value = pages[hop]->values + key[hop];
		if (value->value & LPM_VALUE_FLAG) {
			if (prev_value != LPM_VALUE_GET(value->value)) {
				if (prev_value != LPM_VALUE_INVALID) {
					if (walk_func(
						    key_size,
						    prev_from,
						    prev_to,
						    prev_value,
						    walk_func_data
					    )) {
						return -1;
					}
				}

				prev_value = LPM_VALUE_GET(value->value);
				memcpy(prev_from, key, key_size);
				memset(prev_from + hop + 1,
				       0x00,
				       key_size - hop - 1);
			}
			memcpy(prev_to, key, key_size);
			memset(prev_to + hop + 1, 0xff, key_size - hop - 1);
		} else {
			if (key[hop] == to[hop] && hop == hi_limit)
				++hi_limit;

			++hop;
			if (hop > max_hop) {
				key[hop] = from[hop];
				max_hop = hop;
			} else {
				key[hop] = 0;
			}
			pages[hop] = ADDR_OF(&value->page);
			continue;
		}

		do {
			key[hop]++;
			uint8_t upper_bound = 0xff;
			if (hop == hi_limit)
				upper_bound = to[hop];
			if (key[hop] == (uint8_t)(upper_bound + 1)) {
				if (hop == hi_limit) {
					goto out;
				}
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
			    prev_value,
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
	struct lpm_page *pages[key_size];

	int8_t hop = 0;
	int8_t hi_limit = 0;

	key[hop] = from[hop];
	pages[hop] = lpm_page(lpm, 0);
	int8_t max_hop = 0;

	uint32_t prev_value = LPM_VALUE_INVALID;

	while (1) {
		union lpm_value *value = pages[hop]->values + key[hop];
		if (value->value & LPM_VALUE_FLAG) {
			if (LPM_VALUE_GET(value->value) != prev_value) {
				prev_value = LPM_VALUE_GET(value->value);
				if (collect_func(
					    prev_value, collect_func_data
				    )) {
					return -1;
				}
			}
		} else {
			if (key[hop] == to[hop] && hop == hi_limit)
				++hi_limit;

			++hop;
			if (hop > max_hop) {
				key[hop] = from[hop];
				max_hop = hop;
			} else {
				key[hop] = 0;
			}
			pages[hop] = ADDR_OF(&value->page);
			continue;
		}

		do {
			key[hop]++;

			uint8_t upper_bound = 0xff;
			if (hop == hi_limit)
				upper_bound = to[hop];
			if (key[hop] == (uint8_t)(upper_bound + 1)) {
				if (hop == hi_limit) {
					goto out;
				}
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
	struct lpm_page *pages[key_size];

	int8_t hop = 0;
	key[hop] = 0;
	pages[hop] = lpm_page(lpm, 0);

	while (1) {
		union lpm_value *value = pages[hop]->values + key[hop];
		if (value->value & LPM_VALUE_FLAG) {
			value->value = LPM_VALUE_SET(value_table_get(
				table, 0, LPM_VALUE_GET(value->value)
			));
		} else {
			++hop;
			key[hop] = 0;
			pages[hop] = ADDR_OF(&value->page);
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
	struct lpm_page *pages[key_size];

	int8_t hop = 0;
	key[hop] = 0;
	pages[hop] = lpm_page(lpm, 0);

	while (1) {
		union lpm_value *value = pages[hop]->values + key[hop];
		if (!(value->value & LPM_VALUE_FLAG)) {
			++hop;
			key[hop] = 0;
			pages[hop] = ADDR_OF(&value->page);
			continue;
		}

		do {
			key[hop]++;
			if (key[hop] == 0) {
				if (hop == 0)
					goto out;

				uint64_t first_value =
					pages[hop]->values[0].value;
				bool is_monolite = first_value & LPM_VALUE_FLAG;

				for (uint8_t idx = 255; is_monolite && idx > 0;
				     --idx)
					is_monolite &=
						first_value ==
						pages[hop]->values[idx].value;

				--hop;
				if (is_monolite) {
					pages[hop]->values[key[hop]].value =
						first_value;
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
