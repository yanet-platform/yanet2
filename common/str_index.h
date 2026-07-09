#pragma once

#include <stdint.h>
#include <string.h>

#include "common/hash_index.h"
#include "common/memory.h"

#define STR_INDEX_INVALID HASH_INDEX_INVALID
#define STR_INDEX_INITIAL_CAPACITY 2

// FNV-1a over the first key_size bytes of key (bounded by its NUL), seeded by
// seed. Returns a 64-bit hash; str_index truncates to 32 bits for
// hash_index.
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

static inline uint32_t
str_index_hash(const char *key, uint32_t key_size) {
	uint32_t length = strnlen(key, key_size);
	return (uint32_t)str_index_fnv1a(key, length, 0);
}

// Resolve the key string behind a stored value, for equality checks and
// rehashing.
//
// Returns a NUL-terminated string identifying the entry at index. data is the
// opaque pointer passed alongside the callback from the lookup or insert call
// site.
typedef const char *(*str_index_read_func)(uint32_t index, const void *data);

// Bundle of arguments str_index passes to its hash_index equality adapter.
struct str_index_eq_context {
	const char *key;
	uint32_t key_size;
	str_index_read_func read_func;
	const void *read_func_data;
};

// Equality adapter wrapping str_index's string compare around hash_index's
// generic eq contract.
//
// Returns 0 when the entry at value matches key (via read_func + strncmp), so
// hash_index_lookup stops probing; non-zero keeps probing.
static inline int
str_index_eq(uint32_t value, const void *data) {
	const struct str_index_eq_context *ctx =
		(const struct str_index_eq_context *)data;
	const char *known = ctx->read_func(value, ctx->read_func_data);
	return strncmp(ctx->key, known, ctx->key_size) ? 1 : 0;
}

// String-keyed index built on hash_index, with dynamic resizing.
//
// The embedded hash_index stores caller-supplied uint32_t values resolved by
// string keys; str_index contributes FNV-1a hashing, strncmp equality via a
// read callback, and growth by rehash into a larger fixed-capacity table.
struct str_index {
	struct hash_index hash_index;
};

static inline int
str_index_init(
	struct str_index *str_index, struct memory_context *memory_context
) {
	return hash_index_init(&str_index->hash_index, memory_context, 0);
}

static inline void
str_index_fini(struct str_index *str_index) {
	hash_index_fini(&str_index->hash_index);
}

static inline uint32_t
str_index_lookup(
	const struct str_index *str_index,
	const char *key,
	uint32_t key_size,
	str_index_read_func read_func,
	const void *read_func_data
) {
	struct str_index_eq_context ctx = {
		.key = key,
		.key_size = key_size,
		.read_func = read_func,
		.read_func_data = read_func_data
	};
	return hash_index_lookup(
		&str_index->hash_index,
		str_index_hash(key, key_size),
		str_index_eq,
		&ctx
	);
}

// Move a freshly built hash_index into str_index.
//
// hash_index holds relative pointers (memory_address.h), so a plain struct
// copy would rebind them to the wrong base address; EQUATE_OFFSET re-resolves
// each pointer against the destination field.
static inline void
str_index_take(struct str_index *str_index, struct hash_index *expanded) {
	hash_index_fini(&str_index->hash_index);
	EQUATE_OFFSET(
		&str_index->hash_index.memory_context, &expanded->memory_context
	);
	EQUATE_OFFSET(&str_index->hash_index.entries, &expanded->entries);
	str_index->hash_index.count = expanded->count;
	str_index->hash_index.capacity = expanded->capacity;
}

static inline int
str_index_expand(
	struct str_index *str_index,
	uint32_t key_size,
	uint32_t new_capacity,
	str_index_read_func read_func,
	const void *read_func_data
) {
	struct hash_index *index = &str_index->hash_index;

	const uint32_t *old_entries = ADDR_OF(&index->entries);
	uint32_t old_slots = index->capacity * HASH_INDEX_SPARSE_FACTOR;

	struct hash_index expanded;
	if (hash_index_init(
		    &expanded, ADDR_OF(&index->memory_context), new_capacity
	    ) != 0)
		return -1;

	for (uint32_t pos = 0; pos < old_slots; ++pos) {
		uint32_t value = old_entries[pos];
		if (value == HASH_INDEX_INVALID)
			continue;
		const char *key = read_func(value, read_func_data);
		hash_index_insert(
			&expanded, str_index_hash(key, key_size), value
		);
	}

	str_index_take(str_index, &expanded);
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
	struct hash_index *index = &str_index->hash_index;
	if (index->count >= index->capacity) {
		uint32_t new_capacity = index->capacity
						? index->capacity * 2
						: STR_INDEX_INITIAL_CAPACITY;
		if (str_index_expand(
			    str_index,
			    key_size,
			    new_capacity,
			    read_func,
			    read_func_data
		    ) != 0)
			return -1;
	}

	return hash_index_insert(
		&str_index->hash_index, str_index_hash(key, key_size), value
	);
}
