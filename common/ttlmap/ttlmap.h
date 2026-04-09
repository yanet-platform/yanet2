#pragma once

#include <stddef.h>

#include "detail/bucket.h"
#include "detail/iter.h"
#include "detail/lock.h"
#include "detail/ttlmap.h"

////////////////////////////////////////////////////////////////////////////////

typedef struct ttlmap ttlmap_t;

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

#define TTLMAP_PREFETCH(map_ptr, key_ptr, value_type, ...)                     \
	__TTLMAP_PREFETCH(map_ptr, key_ptr, value_type, ##__VA_ARGS__)

struct ttlmap_bucket_iter {
	size_t buckets;
	size_t next_bucket;
	struct ttlmap *map;
};

static inline void
ttlmap_bucket_iter_init(struct ttlmap_bucket_iter *iter, struct ttlmap *map) {
	iter->map = map;
	iter->next_bucket = 0;
	iter->buckets =
		map->buckets_exp == (size_t)-1 ? 0 : 1ull << map->buckets_exp;
}

#define TTLMAP_ITER_NEXT(iter_ptr, key_type, value_type, now, cb, data)        \
	__extension__({                                                        \
		int __ret = 1;                                                 \
		if ((iter_ptr)->next_bucket == (iter_ptr)->buckets) {          \
			__ret = 0;                                             \
		} else {                                                       \
			__TTLMAP_ITER_NEXT_BUCKET(                             \
				(iter_ptr)->map,                               \
				(iter_ptr)->next_bucket++,                     \
				key_type,                                      \
				value_type,                                    \
				now,                                           \
				cb,                                            \
				data                                           \
			);                                                     \
		}                                                              \
		__ret;                                                         \
	})

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

static inline uint64_t
ttlmap_capacity(ttlmap_t *map) {
	if (map->buckets_exp == (size_t)-1) {
		return 0;
	}
	return (1ull << map->buckets_exp) * __TTLMAP_BUCKET_ENTRIES;
}
