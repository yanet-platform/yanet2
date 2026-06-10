#pragma once

#include <stdint.h>
#include <string.h>

#include "memory.h"

#define STR_INDEX_INVALID 0xffffffff
#define STR_INDEX_SPARSE_FACTOR 4

static inline uint64_t
str_index_fnv1a(const void *key, size_t key_size, uint32_t seed) {
	const uint8_t *data = (const uint8_t *)key;
	uint64_t hash = 14695981039346656037ULL ^ (uint64_t)seed;
	const uint64_t fnv_prime = 1099511628211ULL;

	// Process 4 bytes at a time
	size_t i = 0;
	size_t unroll_limit = key_size & ~3ULL; // Round down to multiple of 4

	for (; i < unroll_limit; i += 4) {
		hash ^= data[i];
		hash *= fnv_prime;
		hash ^= data[i + 1];
		hash *= fnv_prime;
		hash ^= data[i + 2];
		hash *= fnv_prime;
		hash ^= data[i + 3];
		hash *= fnv_prime;
	}

	// Process remaining bytes (0-3 bytes)
	switch (key_size & 3) {
	case 3:
		hash ^= data[i + 2];
		hash *= fnv_prime;
		// fallthrough
	case 2:
		hash ^= data[i + 1];
		hash *= fnv_prime;
		// fallthrough
	case 1:
		hash ^= data[i];
		hash *= fnv_prime;
		// fallthrough
	case 0:
		break;
	}

	return hash;
}

typedef const char *(*str_index_read_func)(uint32_t index, const void *data);

struct str_index {
	struct memory_context *memory_context;
	uint32_t *values;
	uint32_t size;
	uint32_t capacity;
};

static inline int
str_index_init(
	struct str_index *str_index, struct memory_context *memory_context
) {
	SET_OFFSET_OF(&str_index->memory_context, memory_context);

	SET_OFFSET_OF(&str_index->values, NULL);
	str_index->capacity = 0;
	str_index->size = 0;

	return 0;
}

static inline void
str_index_fini(struct str_index *str_index) {
	if (str_index->values == NULL)
		return;

	uint32_t *values = ADDR_OF(&str_index->values);
	memory_bfree(
		ADDR_OF(&str_index->memory_context),
		values,
		sizeof(uint32_t) * str_index->capacity
	);
	SET_OFFSET_OF(&str_index->values, NULL);
}

static inline uint32_t
str_index_lookup(
	const struct str_index *str_index,
	const char *key,
	uint32_t key_size,
	str_index_read_func read_func,
	const void *read_func_data
) {
	if (!str_index->capacity)
		return STR_INDEX_INVALID;

	uint32_t length = strnlen(key, key_size);
	uint32_t hash = str_index_fnv1a(key, length, 0);
	hash = hash % str_index->capacity;

	uint32_t *values = ADDR_OF(&str_index->values);

	while (values[hash] != STR_INDEX_INVALID) {
		const char *known = read_func(values[hash], read_func_data);
		if (!strncmp(key, known, key_size)) {
			break;
		}
		hash = (hash + 1) % str_index->capacity;
	}
	return values[hash];
}

static inline void
str_index_insert_force(
	struct str_index *str_index,
	const char *key,
	uint32_t key_size,
	uint32_t value
) {
	uint32_t length = strnlen(key, key_size);
	uint32_t hash = str_index_fnv1a(key, length, 0);
	hash = hash % str_index->capacity;

	uint32_t *values = ADDR_OF(&str_index->values);

	while (values[hash] != STR_INDEX_INVALID) {
		hash = (hash + 1) % str_index->capacity;
	}
	values[hash] = value;
	str_index->size += 1;
}

static inline int
str_index_expand(
	struct str_index *str_index,
	uint32_t key_size,
	uint32_t new_capacity,
	str_index_read_func read_func,
	const void *read_func_data
) {
	uint32_t *new_values = (uint32_t *)memory_balloc(
		ADDR_OF(&str_index->memory_context),
		sizeof(uint32_t) * new_capacity
	);
	if (new_values == NULL)
		return -1;

	memset(new_values, 0xff, sizeof(uint32_t) * new_capacity);

	uint32_t *old_values = ADDR_OF(&str_index->values);
	uint32_t old_capacity = str_index->capacity;

	SET_OFFSET_OF(&str_index->values, new_values);
	str_index->capacity = new_capacity;

	for (uint32_t pos = 0; pos < old_capacity; ++pos) {
		if (old_values[pos] == STR_INDEX_INVALID)
			continue;
		const char *key = read_func(old_values[pos], read_func_data);
		str_index_insert_force(
			str_index, key, key_size, old_values[pos]
		);
	}

	memory_bfree(
		ADDR_OF(&str_index->memory_context),
		old_values,
		sizeof(uint32_t) * old_capacity
	);
	return 0;
}

static inline int
str_index_insert(
	struct str_index *str_index,
	const char *key,
	uint32_t key_size,
	uint32_t value,
	str_index_read_func read_func,
	const void *read_func_data
) {
	if (str_index->size >= str_index->capacity / STR_INDEX_SPARSE_FACTOR) {
		if (str_index_expand(
			    str_index,
			    key_size,
			    (str_index->size + !str_index->size) *
				    STR_INDEX_SPARSE_FACTOR * 2,
			    read_func,
			    read_func_data
		    )) {
			return -1;
		}
	}

	str_index_insert_force(str_index, key, key_size, value);

	return 0;
}
