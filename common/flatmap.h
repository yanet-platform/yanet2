#pragma once

#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "common/crc32.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "lib/errors/errors.h"

struct flatmap {
	void *keys;
	void *values;
	uint8_t *present;
	size_t capacity;
};

// Releases the storage held by a flat map and resets it to empty.
//
// Safe to call on a map that was never built. The key and value sizes must
// match those used to build the map.
static inline void
flatmap_free(
	struct flatmap *map,
	struct memory_context *mctx,
	size_t key_size,
	size_t value_size
) {
	if (map->keys != NULL) {
		memory_bfree(
			mctx, ADDR_OF(&map->keys), key_size * map->capacity
		);
	}
	if (map->values != NULL) {
		memory_bfree(
			mctx, ADDR_OF(&map->values), value_size * map->capacity
		);
	}
	if (map->present != NULL) {
		memory_bfree(mctx, ADDR_OF(&map->present), map->capacity);
	}
	memset(map, 0, sizeof(*map));
}

// Builds a flat map from arrays of keys and values, using memory from the
// given context.
//
// The capacity must be strictly greater than the number of entries. Passing
// zero entries builds a valid, empty map. Duplicate keys are rejected.
// Returns 0 on success, or -1 with an error added to the given error chain on
// failure.
static inline int
flatmap_build(
	struct flatmap *map,
	struct memory_context *mctx,
	size_t capacity,
	size_t count,
	const void *keys,
	size_t key_size,
	const void *values,
	size_t value_size,
	yanet_error **error
) {
	if (capacity <= count) {
		yanet_error_add(error, "capacity must be greater than count");
		return -1;
	}

	size_t keys_memory = key_size * capacity;
	size_t values_memory = value_size * capacity;
	memset(map, 0, sizeof(*map));

	map->capacity = capacity;

	map->keys = memory_balloc(mctx, keys_memory);
	SET_OFFSET_OF(&map->keys, map->keys);
	if (map->keys == NULL) {
		yanet_error_add(error, "allocation failed");
		return -1;
	}

	map->values = memory_balloc(mctx, values_memory);
	SET_OFFSET_OF(&map->values, map->values);
	if (map->values == NULL) {
		flatmap_free(map, mctx, key_size, value_size);
		yanet_error_add(error, "allocation failed");
		return -1;
	}

	uint8_t *present = memory_balloc(mctx, capacity);
	if (present == NULL) {
		flatmap_free(map, mctx, key_size, value_size);
		yanet_error_add(error, "allocation failed");
		return -1;
	}
	memset(present, 0, capacity);
	SET_OFFSET_OF(&map->present, present);

	for (size_t i = 0; i < count; ++i) {
		const void *key = keys + i * key_size;
		size_t j = crc32(key, key_size, 0) % capacity;
		while (present[j]) {
			const void *map_key =
				ADDR_OF(&map->keys) + j * key_size;
			if (memcmp(key, map_key, key_size) == 0) {
				size_t other = 0;
				while (memcmp(keys + other * key_size,
					      key,
					      key_size) != 0) {
					++other;
				}
				flatmap_free(map, mctx, key_size, value_size);
				yanet_error_add(
					error,
					"key at index %zu matches key at "
					"index %zu",
					i,
					other
				);
				return -1;
			}
			++j;
			if (j == capacity) {
				j = 0;
			}
		}
		present[j] = 1;
		memcpy(ADDR_OF(&map->keys) + j * key_size,
		       keys + i * key_size,
		       key_size);
		memcpy(ADDR_OF(&map->values) + j * value_size,
		       values + i * value_size,
		       value_size);
	}

	return 0;
}

// Looks up the value stored for a key.
//
// Returns a pointer to the value, or NULL if the key is not present. The
// key and value sizes must match those used to build the map.
static inline void *
flatmap_lookup(
	const struct flatmap *map,
	const void *key,
	size_t key_size,
	size_t value_size
) {
	const void *keys = ADDR_OF(&map->keys);
	void *values = ADDR_OF(&map->values);
	uint8_t *present = ADDR_OF(&map->present);
	size_t j = crc32(key, key_size, 0) % map->capacity;
	while (present[j]) {
		if (memcmp(keys + j * key_size, key, key_size) == 0) {
			return values + j * value_size;
		}
		++j;
		if (j == map->capacity) {
			j = 0;
		}
	}
	return NULL;
}