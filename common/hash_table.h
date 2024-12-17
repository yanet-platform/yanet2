#pragma once

#include <stddef.h>
#include <stdint.h>

struct hash_table_chunk {};

struct hash_table {
	size_t length;

	size_t key_size;
	size_t value_size;

	struct hash_table_chunk **chunks;
};

static inline struct hash_table_get_chunk(
	struct hash_table *table, size_t chunk_index *index
) {

}

static inline int
hash_table_put(
	struct hash_table *table,
	uint64_t gen,
	const void *key,
	size_t key_size,
	const void *data,
	size_t data_size
) {
}

static inline int
hash_table_get(
	struct hash_table *table,
	uint64_t gen,
	const void *key,
	size_t key_size,
	void *data,
	size_t data_size
) {
}
