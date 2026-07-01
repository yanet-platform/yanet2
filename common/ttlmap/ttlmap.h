#pragma once

#include <stdbool.h>
#include <stddef.h>

#include "common/memory.h"
#include "detail/bucket.h"
#include "detail/iter.h"
#include "detail/lock.h"
#include "detail/ttlmap.h"

////////////////////////////////////////////////////////////////////////////////

typedef struct ttlmap ttlmap_t;

struct ttlmap_bucket_iter {
	ttlmap_t *map;
	size_t bucket_idx;
	size_t bucket_count;
};

////////////////////////////////////////////////////////////////////////////////

#define TTLMAP_INIT(map_ptr, mctx_ptr, key_type, value_type, kv_entries)       \
	__TTLMAP_INIT_INTERNAL(                                                \
		map_ptr, mctx_ptr, key_type, value_type, kv_entries            \
	)

#define TTLMAP_FREE(map_ptr) __TTLMAP_FREE_INTERNAL(map_ptr)

#define TTLMAP_GET(                                                            \
	map_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now, timeout            \
)                                                                              \
	__TTLMAP_GET_INTERNAL(                                                 \
		map_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now, timeout    \
	)

#define TTLMAP_LOOKUP(map_ptr, key_ptr, value_ptr, now)                        \
	__TTLMAP_LOOKUP_INTERNAL(map_ptr, key_ptr, value_ptr, now)

#define TTLMAP_REMOVE(key_type, value_ptr)                                     \
	__TTLMAP_INVALIDATE_INTERNAL(key_type, value_ptr)

#define TTLMAP_PRINT_STAT(map_ptr, key_type, value_type, fd)                   \
	__TTLMAP_PRINT_STAT_INTERNAL(map_ptr, key_type, value_type, fd)

#define TTLMAP_ITER(map_ptr, key_type, value_type, now, cb, data)              \
	__TTLMAP_ITER_INTERNAL(map_ptr, key_type, value_type, now, cb, data)

#define TTLMAP_ITER_NEXT(iter_ptr, key_type, value_type, now, cb, data)        \
	__TTLMAP_ITER_NEXT_INTERNAL(                                           \
		iter_ptr, key_type, value_type, now, cb, data                  \
	)

#define TTLMAP_PREFETCH(map_ptr, key_ptr, value_type, ...)                     \
	__TTLMAP_PREFETCH(map_ptr, key_ptr, value_type, ##__VA_ARGS__)

////////////////////////////////////////////////////////////////////////////////

static inline void
ttlmap_release_lock(ttlmap_lock_t *lock) {
	__ttlmap_unlock(lock);
}

static inline void
ttlmap_init_empty(ttlmap_t *map) {
	memset(map, 0, sizeof(*map));
	map->buckets_exp = -1;
}

static inline void
ttlmap_bucket_iter_init(struct ttlmap_bucket_iter *iter, ttlmap_t *map) {
	if (iter == NULL) {
		return;
	}

	iter->map = map;
	iter->bucket_idx = 0;
	iter->bucket_count = 0;
	if (map == NULL || map->buckets_exp == (size_t)-1) {
		return;
	}

	iter->bucket_count = 1ull << map->buckets_exp;
}

static inline bool
ttlmap_bucket_iter_done(const struct ttlmap_bucket_iter *iter) {
	if (iter == NULL) {
		return true;
	}
	return iter->bucket_idx >= iter->bucket_count;
}

static inline uint64_t
ttlmap_capacity(ttlmap_t *map) {
	if (map->buckets_exp == (size_t)-1) {
		return 0;
	}
	return (1ull << map->buckets_exp) * __TTLMAP_BUCKET_ENTRIES;
}
