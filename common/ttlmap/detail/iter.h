#pragma once

#include "bucket.h"
#include "ttlmap.h"

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_ITER_INTERNAL(map_ptr, key_type, value_type, now, cb, data)   \
	__extension__({                                                        \
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
				break;                                         \
			}                                                      \
		}                                                              \
	})
