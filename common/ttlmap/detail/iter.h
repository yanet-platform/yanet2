#pragma once

#include "bucket.h"
#include "ttlmap.h"

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_ITER_INTERNAL(map_ptr, key_type, value_type, now, cb, data)   \
	__extension__({                                                        \
		int __ret = 0;                                                 \
		size_t __buckets;                                              \
		if ((map_ptr)->buckets_exp == (uint64_t)-1) {                  \
			__buckets = 0;                                         \
		} else {                                                       \
			__buckets = 1ull << (map_ptr)->buckets_exp;            \
		}                                                              \
		for (size_t __bucket_idx = 0; __bucket_idx < __buckets;        \
		     ++__bucket_idx) {                                         \
			if (__TTLMAP_BUCKET_ITER(                              \
				    map_ptr,                                   \
				    __bucket_idx,                              \
				    key_type,                                  \
				    value_type,                                \
				    now,                                       \
				    cb,                                        \
				    data                                       \
			    ) == 1) {                                          \
				__ret = 1;                                     \
				break;                                         \
			}                                                      \
		}                                                              \
		__ret;                                                         \
	})

////////////////////////////////////////////////////////////////////////////////

/*
 * Weakly consistent: each bucket is copied once under lock. Callback key/value
 * pointers refer to that copy and must not be retained. Callback early-stop
 * advances past the current bucket; later calls continue with the next bucket.
 */

#define __TTLMAP_ITER_NEXT_INTERNAL(                                           \
	iter_ptr, key_type, value_type, now, cb, data                          \
)                                                                              \
	__extension__({                                                        \
		int __ret = 0;                                                 \
		struct ttlmap_bucket_iter *__iter = (iter_ptr);                \
		if (__iter != NULL && __iter->map != NULL &&                   \
		    __iter->bucket_idx < __iter->bucket_count) {               \
			__auto_type __now = (now);                             \
			__auto_type __cb = (cb);                               \
			__auto_type __data = (data);                           \
			while (__iter->bucket_idx < __iter->bucket_count) {    \
				size_t __bucket_idx = __iter->bucket_idx;      \
				++__iter->bucket_idx;                          \
				if (__TTLMAP_BUCKET_ITER_NEXT(                 \
					    __iter->map,                       \
					    __bucket_idx,                      \
					    key_type,                          \
					    value_type,                        \
					    __now,                             \
					    __cb,                              \
					    __data                             \
				    ) == 1) {                                  \
					__ret = 1;                             \
					break;                                 \
				}                                              \
			}                                                      \
		}                                                              \
		__ret;                                                         \
	})
