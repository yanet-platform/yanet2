#include "map.h"
#include "common/big_array.h"
#include "common/btree/u64.h"
#include <stdlib.h>
#include <string.h>

static int
cmp_kv(const void *left, const void *right) {
	const struct key_value *left_entry = (const struct key_value *)left;
	const struct key_value *right_entry = (const struct key_value *)right;
	return left_entry->key - right_entry->key;
};

static inline void
set(struct big_array *array, size_t idx, size_t value) {
	memcpy(big_array_get(array, idx * sizeof(size_t)),
	       &value,
	       sizeof(size_t));
}

int
map_init(
	struct map *map,
	struct memory_context *mctx,
	struct key_value *entries,
	size_t entry_count
) {
	if (big_array_init(&map->keys, sizeof(size_t) * entry_count, mctx) !=
	    0) {
		return -1;
	}
	if (big_array_init(&map->values, sizeof(size_t) * entry_count, mctx) !=
	    0) {
		big_array_free(&map->keys);
		return -1;
	}
	qsort((void *)entries, entry_count, sizeof(struct key_value), cmp_kv);
	uint64_t *keys = malloc(sizeof(uint64_t) * entry_count);
	for (size_t i = 0; i < entry_count; ++i) {
		keys[i] = entries[i].key;
	}
	if (btree_u64_init(&map->btree, keys, entry_count, mctx) != 0) {
		big_array_free(&map->keys);
		big_array_free(&map->values);
		free(keys);
		return -1;
	}
	for (size_t i = 0; i < entry_count; ++i) {
		set(&map->keys, i, entries[i].key);
		set(&map->values, i, entries[i].value);
	}
	free(keys);
	return 0;
}

void
map_free(struct map *map) {
	big_array_free(&map->keys);
	big_array_free(&map->values);
	btree_u64_free(&map->btree);
}

size_t
map_memory_usage(struct map *map) {
	return big_array_memory_usage(&map->keys) +
	       big_array_memory_usage(&map->values) +
	       btree_u64_memory_usage(&map->btree);
}