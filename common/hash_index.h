#pragma once

#include <stdint.h>
#include <string.h>

#include "memory.h"

#define HASH_INDEX_INVALID 0xffffffff
#define HASH_INDEX_SPARSE_FACTOR 4

// Equality predicate invoked during hash_index_lookup.
//
// Called for every probed slot until it returns 0 (match) or an empty slot
// is reached. value is the slot payload, typically an index into an
// external values array. data is the opaque pointer passed alongside the
// callback from the lookup call site. Return 0 to accept the slot,
// non-zero to keep probing.
typedef int (*hash_index_eq_func)(uint32_t value, const void *data);

struct hash_index {
	struct memory_context *memory_context;
	uint32_t *entries;
	uint32_t count;
	uint32_t capacity;
};

// Allocate a fixed-capacity hash index backed by memory_context.
static inline int
hash_index_init(
	struct hash_index *hash_index,
	struct memory_context *memory_context,
	uint32_t capacity
) {
	SET_OFFSET_OF(&hash_index->memory_context, memory_context);

	if (capacity == 0) {
		SET_OFFSET_OF(&hash_index->entries, NULL);
		hash_index->capacity = 0;
		hash_index->count = 0;
		return 0;
	}

	uint32_t *entries = (uint32_t *)memory_balloc(
		ADDR_OF(&hash_index->memory_context),
		sizeof(uint32_t) * capacity * HASH_INDEX_SPARSE_FACTOR
	);
	if (entries == NULL) {
		SET_OFFSET_OF(&hash_index->entries, NULL);
		hash_index->capacity = 0;
		hash_index->count = 0;
		return -1;
	}

	memset(entries,
	       0xff,
	       sizeof(uint32_t) * capacity * HASH_INDEX_SPARSE_FACTOR);

	SET_OFFSET_OF(&hash_index->entries, entries);
	hash_index->capacity = capacity;
	hash_index->count = 0;

	return 0;
}

static inline void
hash_index_fini(struct hash_index *hash_index) {
	uint32_t *entries = ADDR_OF(&hash_index->entries);
	if (entries == NULL) {
		return;
	}

	memory_bfree(
		ADDR_OF(&hash_index->memory_context),
		entries,
		sizeof(uint32_t) * hash_index->capacity *
			HASH_INDEX_SPARSE_FACTOR
	);
	SET_OFFSET_OF(&hash_index->entries, NULL);
	hash_index->capacity = 0;
	hash_index->count = 0;
}

static inline uint32_t
hash_index_lookup(
	const struct hash_index *hash_index,
	uint32_t hash,
	hash_index_eq_func eq_func,
	const void *eq_func_data
) {
	if (!hash_index->capacity) {
		return HASH_INDEX_INVALID;
	}

	uint32_t bucket =
		hash % (hash_index->capacity * HASH_INDEX_SPARSE_FACTOR);
	const uint32_t *entries = ADDR_OF(&hash_index->entries);

	while (entries[bucket] != HASH_INDEX_INVALID) {
		if (eq_func(entries[bucket], eq_func_data) == 0) {
			return entries[bucket];
		}
		bucket = (bucket + 1) %
			 (hash_index->capacity * HASH_INDEX_SPARSE_FACTOR);
	}
	return HASH_INDEX_INVALID;
}

// Place value into the first free slot at or after hash % capacity.
//
// The index is fixed-capacity; insert returns -1 when it is full, 0 on
// success.
static inline int
hash_index_insert(
	struct hash_index *hash_index, uint32_t hash, uint32_t value
) {
	if (hash_index->count >= hash_index->capacity) {
		return -1;
	}

	uint32_t bucket =
		hash % (hash_index->capacity * HASH_INDEX_SPARSE_FACTOR);
	uint32_t *entries = ADDR_OF(&hash_index->entries);

	while (entries[bucket] != HASH_INDEX_INVALID) {
		bucket = (bucket + 1) %
			 (hash_index->capacity * HASH_INDEX_SPARSE_FACTOR);
	}

	entries[bucket] = value;
	hash_index->count += 1;

	return 0;
}
