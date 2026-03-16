#pragma once

#include "common/big_array.h"
#include "common/btree/u64.h"
#include "common/likely.h"

struct map {
	struct btree_u64 btree;
	struct big_array keys;
	struct big_array values;
};

struct key_value {
	size_t key;
	size_t value;
};

int
map_init(
	struct map *map,
	struct memory_context *mctx,
	struct key_value *entries,
	size_t entry_count
);

void
map_free(struct map *map);

static inline int
map_find(struct map *map, size_t key, size_t *value) {
	size_t lb = btree_u64_lower_bound(&map->btree, key);
	if (unlikely(lb == map->btree.n)) {
		return -1;
	}
	size_t lb_key =
		*(size_t *)big_array_get(&map->keys, lb * sizeof(size_t));
	if (lb_key == key) {
		*value = *(size_t *)big_array_get(
			&map->values, lb * sizeof(size_t)
		);
		return 0;
	} else {
		return -1;
	}
}

size_t
map_memory_usage(struct map *map);