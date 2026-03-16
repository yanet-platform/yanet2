#pragma once

#include "common/big_array.h"
#include "common/btree/u64.h"

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

int
map_find(struct map *map, size_t key, size_t *value);