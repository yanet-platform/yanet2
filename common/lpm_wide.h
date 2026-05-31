#pragma once

#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

#include <string.h>

#include "memory.h"
#include "memory_address.h"
#include "value.h"

#define LPM_WIDE_VALUE_INVALID 0xffffffff
#define LPM_WIDE_VALUE_FLAG 0x00000001
#define LPM_WIDE_VALUE_SET(value) ((value << 1) | LPM_WIDE_VALUE_FLAG)
#define LPM_WIDE_VALUE_GET(value) (value >> 1)
#define LPM_WIDE_CHUNK_SIZE 4
#define LPM_WIDE_PAGE_SIZE 65536
#define LPM_WIDE_KEY_SIZE_MAX 16

struct lpm_wide_page;

union lpm_wide_value {
	struct lpm_wide_page *page;
	uint64_t value;
};

struct lpm_wide_page {
	union lpm_wide_value values[LPM_WIDE_PAGE_SIZE];
};

struct lpm_wide {
	struct memory_context memory_context;
	struct lpm_wide_page **pages;
	size_t page_count;
};

static inline struct lpm_wide_page *
lpm_wide_page(const struct lpm_wide *lpm, uint32_t page_idx) {
	struct lpm_wide_page **pages = ADDR_OF(&lpm->pages);
	struct lpm_wide_page *chunk =
		ADDR_OF(&pages[page_idx / LPM_WIDE_CHUNK_SIZE]);
	return chunk + page_idx % LPM_WIDE_CHUNK_SIZE;
}

static inline int
lpm_wide_new_page(struct lpm_wide *lpm, union lpm_wide_value *value) {
	if (!(lpm->page_count % LPM_WIDE_CHUNK_SIZE)) {
		uint32_t old_chunk_count =
			lpm->page_count / LPM_WIDE_CHUNK_SIZE;
		uint32_t new_chunk_count = old_chunk_count + 1;

		struct memory_context *memory_context = &lpm->memory_context;

		struct lpm_wide_page **pages =
			(struct lpm_wide_page **)memory_balloc(
				memory_context,
				sizeof(struct lpm_wide_page *) * new_chunk_count
			);
		if (pages == NULL) {
			errno = ENOMEM;
			return -1;
		}

		struct lpm_wide_page *page =
			(struct lpm_wide_page *)memory_balloc(
				memory_context,
				sizeof(struct lpm_wide_page) *
					LPM_WIDE_CHUNK_SIZE
			);
		if (page == NULL) {
			memory_bfree(
				memory_context,
				pages,
				sizeof(struct lpm_wide_page *) * new_chunk_count
			);

			errno = ENOMEM;
			return -1;
		}

		struct lpm_wide_page **old_pages = ADDR_OF(&lpm->pages);
		for (uint64_t chunk_idx = 0; chunk_idx < old_chunk_count;
		     ++chunk_idx) {
			EQUATE_OFFSET(&pages[chunk_idx], &old_pages[chunk_idx]);
		}

		SET_OFFSET_OF(&pages[old_chunk_count], page);
		SET_OFFSET_OF(&lpm->pages, pages);

		memory_bfree(
			memory_context,
			old_pages,
			old_chunk_count * sizeof(struct lpm_wide_page *)
		);
	}
	struct lpm_wide_page *np = lpm_wide_page(lpm, lpm->page_count);
	lpm->page_count += 1;

	if (value != NULL) {
		for (size_t i = 0; i < LPM_WIDE_PAGE_SIZE; i++) {
			np->values[i].value = value->value;
		}

		SET_OFFSET_OF(
			&value->page, lpm_wide_page(lpm, lpm->page_count - 1)
		);
	} else {
		memset(np, 0xff, sizeof(struct lpm_wide_page));
	}

	return 0;
}

static inline int
lpm_wide_init(struct lpm_wide *lpm, struct memory_context *memory_context) {
	memory_context_init_from(
		&lpm->memory_context, memory_context, "lpm_wide"
	);
	lpm->pages = NULL;
	lpm->page_count = 0;
	return lpm_wide_new_page(lpm, NULL);
}

static inline void
lpm_wide_free(struct lpm_wide *lpm) {
	struct memory_context *memory_context = &lpm->memory_context;
	struct lpm_wide_page **pages = ADDR_OF(&lpm->pages);

	if (pages != NULL) {
		uint32_t chunk_count =
			(lpm->page_count + LPM_WIDE_CHUNK_SIZE - 1) /
			LPM_WIDE_CHUNK_SIZE;

		for (size_t chunk_idx = 0; chunk_idx < chunk_count;
		     ++chunk_idx) {
			memory_bfree(
				&lpm->memory_context,
				ADDR_OF(&pages[chunk_idx]),
				sizeof(struct lpm_wide_page) *
					LPM_WIDE_CHUNK_SIZE
			);
		}

		memory_bfree(
			memory_context,
			pages,
			sizeof(struct lpm_wide_page *) * chunk_count
		);
	}

	memory_context_fini(memory_context);
}

static inline int
lpm_wide_check_key_size(uint8_t key_size) {
	if (key_size % 2 == 1) {
		errno = EINVAL;
		return -1;
	}

	return 0;
}

static inline void
lpm_wide_key_from_be(uint8_t key_size, const uint8_t *key, uint16_t *words) {
	for (size_t word_idx = 0; word_idx < key_size / 2; ++word_idx) {
		words[word_idx] = ((uint16_t)key[word_idx * 2] << 8) |
				  key[word_idx * 2 + 1];
	}
}

static inline void
lpm_wide_key_to_be(uint8_t word_size, const uint16_t *words, uint8_t *key) {
	for (size_t word_idx = 0; word_idx < word_size; ++word_idx) {
		key[word_idx * 2] = words[word_idx] >> 8;
		key[word_idx * 2 + 1] = words[word_idx] & 0xff;
	}
}

static inline int
lpm_wide_key_cmp(uint8_t word_size, const uint16_t *l, const uint16_t *r) {
	for (size_t i = 0; i < word_size; ++i) {
		if (l[i] < r[i])
			return -1;
		if (l[i] > r[i])
			return 1;
	}
	return 0;
}

static inline int
lpm_wide_check_range_lo(
	uint8_t word_size,
	const uint16_t *key,
	const uint16_t *from,
	uint8_t hop
) {
	uint16_t check[word_size];
	memcpy(check, key, (hop + 1) * sizeof(uint16_t));

	if (hop + 1 < word_size)
		memset(check + hop + 1,
		       0x00,
		       (word_size - hop - 1) * sizeof(uint16_t));
	if (lpm_wide_key_cmp(word_size, check, from) < 0)
		return -1;

	return 0;
}

static inline int
lpm_wide_check_range_hi(
	uint8_t word_size, const uint16_t *key, const uint16_t *to, uint8_t hop
) {
	uint16_t check[word_size];
	memcpy(check, key, word_size * sizeof(uint16_t));

	if (hop + 1 < word_size) {
		for (size_t word_idx = hop + 1; word_idx < word_size;
		     ++word_idx) {
			check[word_idx] = 0xffff;
		}
	}
	if (lpm_wide_key_cmp(word_size, check, to) > 0)
		return -1;

	return 0;
}

static inline int
lpm_wide_insert(
	struct lpm_wide *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
) {
	if (lpm_wide_check_key_size(key_size))
		return -1;

	uint8_t word_size = key_size / 2;

	uint16_t from_words[word_size];
	lpm_wide_key_from_be(key_size, from, from_words);

	uint16_t to_words[word_size];
	lpm_wide_key_from_be(key_size, to, to_words);

	uint16_t key[word_size];
	struct lpm_wide_page *pages[word_size];

	int16_t hop = 0;
	key[hop] = from_words[hop];
	pages[hop] = lpm_wide_page(lpm, 0);
	int16_t max_hop = 0;

	while (1) {
		union lpm_wide_value *stored_value =
			pages[hop]->values + key[hop];
		if (stored_value->value & LPM_WIDE_VALUE_FLAG) {
			if (hop < word_size - 1 &&
			    (lpm_wide_check_range_lo(
				     word_size, key, from_words, hop
			     ) ||
			     lpm_wide_check_range_hi(
				     word_size, key, to_words, hop
			     ))) {
				if (lpm_wide_new_page(lpm, stored_value))
					return -1;
				++hop;
				if (hop > max_hop) {
					key[hop] = from_words[hop];
					max_hop = hop;
				} else {
					key[hop] = 0;
				}
				pages[hop] = ADDR_OF(&stored_value->page);
				continue;
			} else {
				stored_value->value = LPM_WIDE_VALUE_SET(value);
			}
		} else {
			++hop;
			if (hop > max_hop) {
				key[hop] = from_words[hop];
				max_hop = hop;
			} else {
				key[hop] = 0;
			}
			pages[hop] = ADDR_OF(&stored_value->page);
			continue;
		}

		do {
			uint32_t next = (uint32_t)key[hop] + 1;
			uint16_t upper_bound = 0xffff;
			if (next <= 0xffff) {
				key[hop] = next;
				if (lpm_wide_check_range_hi(
					    word_size, key, to_words, hop
				    )) {
					upper_bound = to_words[hop];
				}
			}
			if (next > upper_bound) {
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
lpm_wide_lookup(
	const struct lpm_wide *lpm, uint8_t key_size, const uint8_t *key
) {
	if (lpm_wide_check_key_size(key_size))
		return LPM_WIDE_VALUE_INVALID;

	uint8_t word_size = key_size / 2;
	uint16_t key_words[word_size];
	lpm_wide_key_from_be(key_size, key, key_words);

	union lpm_wide_value *value = NULL;
	struct lpm_wide_page *page = lpm_wide_page(lpm, 0);

	for (uint8_t hop = 0; hop < word_size; ++hop) {
		value = page->values + key_words[hop];
		if (value->value & LPM_WIDE_VALUE_FLAG)
			break;
		page = ADDR_OF(&value->page);
	}

	return LPM_WIDE_VALUE_GET(value->value);
}

static inline size_t
lpm_wide_memory_usage(struct lpm_wide *lpm) {
	return lpm->memory_context.balloc_size - lpm->memory_context.bfree_size;
}

typedef int (*lpm_wide_walk_func)(
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value,
	void *data
);

static inline int
lpm_wide_walk(
	const struct lpm_wide *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	lpm_wide_walk_func walk_func,
	void *walk_func_data
) {
	if (lpm_wide_check_key_size(key_size))
		return -1;

	uint8_t word_size = key_size / 2;

	uint16_t from_words[word_size];
	lpm_wide_key_from_be(key_size, from, from_words);

	uint16_t to_words[word_size];
	lpm_wide_key_from_be(key_size, to, to_words);

	uint16_t key[word_size];
	memset(key, 0, word_size * sizeof(uint16_t));
	struct lpm_wide_page *pages[word_size];

	int16_t hop = 0;
	int16_t hi_limit = 0;

	key[hop] = from_words[hop];
	pages[hop] = lpm_wide_page(lpm, 0);
	int16_t max_hop = 0;

	uint32_t prev_value = LPM_WIDE_VALUE_INVALID;
	uint16_t prev_from[word_size];
	memcpy(prev_from, from_words, word_size * sizeof(uint16_t));
	uint16_t prev_to[word_size];

	while (1) {
		union lpm_wide_value *value = pages[hop]->values + key[hop];
		if (value->value & LPM_WIDE_VALUE_FLAG) {
			if (prev_value != LPM_WIDE_VALUE_GET(value->value)) {
				if (prev_value != LPM_WIDE_VALUE_INVALID) {
					uint8_t prev_from_bytes[key_size];
					uint8_t prev_to_bytes[key_size];
					lpm_wide_key_to_be(
						word_size,
						prev_from,
						prev_from_bytes
					);
					lpm_wide_key_to_be(
						word_size,
						prev_to,
						prev_to_bytes
					);
					if (walk_func(
						    key_size,
						    prev_from_bytes,
						    prev_to_bytes,
						    prev_value,
						    walk_func_data
					    )) {
						return -1;
					}
				}

				prev_value = LPM_WIDE_VALUE_GET(value->value);
				memcpy(prev_from,
				       key,
				       word_size * sizeof(uint16_t));
				for (size_t word_idx = hop + 1;
				     word_idx < word_size;
				     ++word_idx) {
					prev_from[word_idx] = 0;
				}
			}
			memcpy(prev_to, key, word_size * sizeof(uint16_t));
			for (size_t word_idx = hop + 1; word_idx < word_size;
			     ++word_idx) {
				prev_to[word_idx] = 0xffff;
			}
		} else {
			if (key[hop] == to_words[hop] && hop == hi_limit)
				++hi_limit;

			++hop;
			if (hop > max_hop) {
				key[hop] = from_words[hop];
				max_hop = hop;
			} else {
				key[hop] = 0;
			}
			pages[hop] = ADDR_OF(&value->page);
			continue;
		}

		do {
			uint32_t next = (uint32_t)key[hop] + 1;
			uint16_t upper_bound = 0xffff;
			if (hop == hi_limit)
				upper_bound = to_words[hop];
			if (next > upper_bound) {
				if (hop == hi_limit) {
					goto out;
				}
				--hop;
			} else {
				key[hop] = next;
				break;
			}
		} while (1);
	}

out:

	if (prev_value != LPM_WIDE_VALUE_INVALID) {
		uint8_t prev_from_bytes[key_size];
		lpm_wide_key_to_be(word_size, prev_from, prev_from_bytes);

		uint8_t prev_to_bytes[key_size];
		lpm_wide_key_to_be(word_size, prev_to, prev_to_bytes);

		if (walk_func(
			    key_size,
			    prev_from_bytes,
			    prev_to_bytes,
			    prev_value,
			    walk_func_data
		    )) {
			return -1;
		}
	}

	return 0;
}

typedef int (*lpm_wide_collect_values_func)(uint32_t value, void *data);

static inline int
lpm_wide_collect_values(
	const struct lpm_wide *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	lpm_wide_collect_values_func collect_func,
	void *collect_func_data
) {
	if (lpm_wide_check_key_size(key_size))
		return -1;

	uint8_t word_size = key_size / 2;

	uint16_t from_words[word_size];
	lpm_wide_key_from_be(key_size, from, from_words);

	uint16_t to_words[word_size];
	lpm_wide_key_from_be(key_size, to, to_words);

	uint16_t key[word_size];
	struct lpm_wide_page *pages[word_size];

	int16_t hop = 0;
	int16_t hi_limit = 0;

	key[hop] = from_words[hop];
	pages[hop] = lpm_wide_page(lpm, 0);
	int16_t max_hop = 0;

	uint32_t prev_value = LPM_WIDE_VALUE_INVALID;

	while (1) {
		union lpm_wide_value *value = pages[hop]->values + key[hop];
		if (value->value & LPM_WIDE_VALUE_FLAG) {
			if (LPM_WIDE_VALUE_GET(value->value) != prev_value) {
				prev_value = LPM_WIDE_VALUE_GET(value->value);
				if (collect_func(
					    prev_value, collect_func_data
				    )) {
					return -1;
				}
			}
		} else {
			if (key[hop] == to_words[hop] && hop == hi_limit)
				++hi_limit;

			++hop;
			if (hop > max_hop) {
				key[hop] = from_words[hop];
				max_hop = hop;
			} else {
				key[hop] = 0;
			}
			pages[hop] = ADDR_OF(&value->page);
			continue;
		}

		do {
			uint32_t next = (uint32_t)key[hop] + 1;

			uint16_t upper_bound = 0xffff;
			if (hop == hi_limit)
				upper_bound = to_words[hop];
			if (next > upper_bound) {
				if (hop == hi_limit) {
					goto out;
				}
				--hop;
			} else {
				key[hop] = next;
				break;
			}

		} while (1);
	}

out:

	return 0;
}

static inline void
lpm_wide_remap(
	struct lpm_wide *lpm, uint8_t key_size, struct value_table *table
) {
	if (lpm_wide_check_key_size(key_size))
		return;

	uint8_t word_size = key_size / 2;
	uint16_t key[word_size];
	struct lpm_wide_page *pages[word_size];

	int16_t hop = 0;
	key[hop] = 0;
	pages[hop] = lpm_wide_page(lpm, 0);

	while (1) {
		union lpm_wide_value *value = pages[hop]->values + key[hop];
		if (value->value & LPM_WIDE_VALUE_FLAG) {
			value->value = LPM_WIDE_VALUE_SET(value_table_get(
				table, 0, LPM_WIDE_VALUE_GET(value->value)
			));
		} else {
			++hop;
			key[hop] = 0;
			pages[hop] = ADDR_OF(&value->page);
			continue;
		}

		do {
			uint32_t next = (uint32_t)key[hop] + 1;
			if (next > 0xffff) {
				if (hop == 0)
					goto out;
				--hop;
			} else {
				key[hop] = next;
				break;
			}
		} while (1);
	}

out:
	return;
}

static inline void
lpm_wide_compact(struct lpm_wide *lpm, uint8_t key_size) {
	if (lpm_wide_check_key_size(key_size))
		return;

	uint8_t word_size = key_size / 2;
	uint16_t key[word_size];
	struct lpm_wide_page *pages[word_size];

	int16_t hop = 0;
	key[hop] = 0;
	pages[hop] = lpm_wide_page(lpm, 0);

	while (1) {
		union lpm_wide_value *value = pages[hop]->values + key[hop];
		if (!(value->value & LPM_WIDE_VALUE_FLAG)) {
			++hop;
			key[hop] = 0;
			pages[hop] = ADDR_OF(&value->page);
			continue;
		}

		do {
			uint32_t next = (uint32_t)key[hop] + 1;
			if (next > 0xffff) {
				if (hop == 0)
					goto out;

				uint64_t first_value =
					pages[hop]->values[0].value;
				bool is_monolite =
					first_value & LPM_WIDE_VALUE_FLAG;

				for (size_t idx = 1;
				     is_monolite && idx < LPM_WIDE_PAGE_SIZE;
				     ++idx) {
					is_monolite &=
						first_value ==
						pages[hop]->values[idx].value;
				}

				--hop;
				if (is_monolite) {
					pages[hop]->values[key[hop]].value =
						first_value;
				}
			} else {
				key[hop] = next;
				break;
			}
		} while (1);
	}

out:
	return;
}
