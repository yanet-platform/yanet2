#pragma once

#include "chunk.h"

#include <stddef.h>

#include "../../memory.h"
#include "../../memory_address.h"
#include "../../memory_block.h"

#include "bucket.h"

////////////////////////////////////////////////////////////////////////////////

typedef struct ttlmap {
	struct memory_context mctx;
	void *chunks[__TTLMAP_MAX_CHUNKS]; // relative pointers
	size_t chunk_shifts[__TTLMAP_MAX_CHUNKS];
	size_t chunk_sizes[__TTLMAP_MAX_CHUNKS];
	size_t buckets_per_chunk_exp; // buckets_per_chunk =
				      // 2**buckets_per_chunk_exp
	size_t buckets_exp;	      // buckets = 2**buckets_exp
} __attribute__((__aligned__(64))) ttlmap_t;

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_LOOKUP_INTERNAL(map_ptr, key_ptr, value_ptr, now)             \
	__extension__({                                                        \
		uint32_t __hash = __TTLMAP_KEY_HASH((key_ptr));                \
		uint32_t __buckets = 1 << ((map_ptr)->buckets_exp);            \
		uint32_t __bucket_id = __hash & (__buckets - 1);               \
		uint32_t __chunk =                                             \
			__bucket_id >> ((map_ptr)->buckets_per_chunk_exp);     \
		uint32_t __buckets_per_chunk =                                 \
			1 << ((map_ptr)->buckets_per_chunk_exp);               \
		uint32_t __bucket_in_chunk =                                   \
			__bucket_id & (__buckets_per_chunk - 1);               \
		void *__b = __extension__({                                    \
			typedef typeof(*(key_ptr)) __key_type;                 \
			typedef typeof(*(value_ptr)) __value_type;             \
			__TTLMAP_BUCKET_DECLARE(__key_type, __value_type);     \
			__bucket_t *__buckets_array =                          \
				ADDR_OF(&((map_ptr)->chunks[__chunk]));        \
			&__buckets_array[__bucket_in_chunk];                   \
		});                                                            \
		uint32_t __idx =                                               \
			(__hash >> ((map_ptr)->buckets_exp) &                  \
			 (__TTLMAP_BUCKET_ENTRIES - 1));                       \
		__TTLMAP_BUCKET_LOOKUP(                                        \
			__b, (key_ptr), (value_ptr), (now), __idx              \
		);                                                             \
	})

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_GET_INTERNAL(                                                 \
	map_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now, timeout            \
)                                                                              \
	__extension__({                                                        \
		uint32_t __hash = __TTLMAP_KEY_HASH((key_ptr));                \
		uint32_t __buckets = 1 << ((map_ptr)->buckets_exp);            \
		uint32_t __bucket_id = __hash & (__buckets - 1);               \
		uint32_t __chunk =                                             \
			__bucket_id >> ((map_ptr)->buckets_per_chunk_exp);     \
		uint32_t __buckets_per_chunk =                                 \
			1 << ((map_ptr)->buckets_per_chunk_exp);               \
		uint32_t __bucket_in_chunk =                                   \
			__bucket_id & (__buckets_per_chunk - 1);               \
		void *__b = __extension__({                                    \
			typedef typeof(*(key_ptr)) __key_type;                 \
			typedef typeof(**(value_ptr_ptr)) __value_type;        \
			__TTLMAP_BUCKET_DECLARE(__key_type, __value_type);     \
			__bucket_t *__buckets_array =                          \
				ADDR_OF(&((map_ptr)->chunks[__chunk]));        \
			&__buckets_array[__bucket_in_chunk];                   \
		});                                                            \
		uint32_t __idx =                                               \
			(__hash >> ((map_ptr)->buckets_exp) &                  \
			 (__TTLMAP_BUCKET_ENTRIES - 1));                       \
		__TTLMAP_BUCKET_GET(                                           \
			__b,                                                   \
			(key_ptr),                                             \
			(value_ptr_ptr),                                       \
			(lock_ptr_ptr),                                        \
			(now),                                                 \
			(timeout),                                             \
			__idx                                                  \
		);                                                             \
	})

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_FREE_INTERNAL(map_ptr)                                        \
	__extension__({                                                        \
		for (size_t __i = 0; __i < __TTLMAP_MAX_CHUNKS; ++__i) {       \
			if ((map_ptr)->chunks[__i] != NULL) {                  \
				memory_bfree(                                  \
					&(map_ptr)->mctx,                      \
					ADDR_OF(&(map_ptr)->chunks[__i]),      \
					(map_ptr)->chunk_sizes[__i]            \
				);                                             \
			}                                                      \
		}                                                              \
		(map_ptr)->buckets_exp = (size_t)-1;                           \
	})

static inline int
__ttlmap_init_internal( // NOLINT
	ttlmap_t *map,
	struct memory_context *mctx,
	size_t bucket_align,
	size_t bucket_size,
	size_t bucket_count
) {
	if ((bucket_count & (bucket_count - 1)) !=
	    0) { // bucket count must be power of 2
		return -1;
	}

	int res = memory_context_init_from(&map->mctx, mctx, "ttlmap");
	if (res < 0) {
		return -1;
	}

	map->buckets_exp = 63 - __builtin_clzll(bucket_count);

	// coarse to the closest power of two
	map->buckets_per_chunk_exp =
		63 - __builtin_clzll(
			     (MEMORY_BLOCK_ALLOCATOR_MAX_SIZE - bucket_align) /
			     bucket_size
		     );
	size_t buckets_per_chunk = 1ull << map->buckets_per_chunk_exp;

	memset(&map->chunks, 0, sizeof(map->chunks));
	memset(&map->chunk_sizes, 0, sizeof(map->chunk_sizes));

	for (size_t i = 0; i < __TTLMAP_MAX_CHUNKS; ++i) {
		if (bucket_count == 0) {
			break;
		}
		size_t need_size = bucket_count * bucket_size + bucket_align;
		if (need_size > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE) {
			need_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE;
		}
		void *chunk = memory_balloc(&map->mctx, need_size);
		if (chunk == NULL) {
			break;
		}
		uintptr_t chunk_ptr = (uintptr_t)chunk;
		size_t need_add_offset =
			(bucket_align - chunk_ptr % bucket_align) %
			bucket_align;
		map->chunk_shifts[i] = need_add_offset;
		map->chunk_sizes[i] = need_size;
		chunk_ptr += need_add_offset;
		SET_OFFSET_OF(&map->chunks[i], (void *)chunk_ptr);
		if (buckets_per_chunk >= bucket_count) {
			bucket_count = 0;
		} else {
			bucket_count -= buckets_per_chunk;
		}
	}

	if (bucket_count != 0) {
		__TTLMAP_FREE_INTERNAL(map);
		return -1;
	}

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_INIT_INTERNAL(                                                \
	map_ptr, mctx_ptr, key_type, value_type, entries                       \
)                                                                              \
	__extension__({                                                        \
		__label__ __done;                                              \
		int __res = 0;                                                 \
		__TTLMAP_BUCKET_DECLARE(key_type, value_type);                 \
		size_t __bucket_count = __ttlmap_bucket_count(entries);        \
		assert(__bucket_count > 0);                                    \
		__res = __ttlmap_init_internal(                                \
			map_ptr,                                               \
			mctx_ptr,                                              \
			alignof(__bucket_t),                                   \
			sizeof(__bucket_t),                                    \
			__bucket_count                                         \
		);                                                             \
		if (__res < 0) {                                               \
			goto __done;                                           \
		}                                                              \
		for (size_t __i = 0; __i < __bucket_count; ++__i) {            \
			__bucket_t *__b = __TTLMAP_BUCKET_FIND_WITH_ID(        \
				map_ptr, __i, key_type, value_type             \
			);                                                     \
			__TTLMAP_BUCKET_INIT(__b, key_type, value_type);       \
		}                                                              \
	__done:                                                                \
		__res;                                                         \
	})

////////////////////////////////////////////////////////////////////////////////

#include <stdio.h>

#define __TTLMAP_PRINT_STAT_INTERNAL(map_ptr, key_type, value_type, fd)        \
	__extension__({                                                        \
		size_t __bucket_size = __extension__({                         \
			__TTLMAP_BUCKET_DECLARE(key_type, value_type);         \
			sizeof(__bucket_t);                                    \
		});                                                            \
		FILE *__file = fdopen(fd, "w");                                \
		fprintf(__file, "======= ttlmap stat =======\n");              \
		fprintf(__file, "\tKey size: %lu bytes\n", sizeof(key_type));  \
		fprintf(__file,                                                \
			"\tValue size: %lu bytes\n",                           \
			sizeof(value_type));                                   \
		fprintf(__file, "\tBucket size: %lu bytes\n", __bucket_size);  \
		fprintf(__file,                                                \
			"\tMemory used: %lu bytes\n",                          \
			(map_ptr)->mctx.balloc_size);                          \
		fprintf(__file,                                                \
			"\tKey-Value pairs per Bucket: %u\n",                  \
			__TTLMAP_BUCKET_ENTRIES);                              \
		fprintf(__file,                                                \
			"\tNumber of Buckets: %llu\n",                         \
			(1ull << (map_ptr)->buckets_exp));                     \
		fprintf(__file,                                                \
			"\tPer Bucker memory overhead: %.2lf%%\n",             \
			100.0 * (double)__bucket_size /                        \
				(__TTLMAP_BUCKET_ENTRIES *                     \
				 (sizeof(key_type) + sizeof(value_type))));    \
		fprintf(__file,                                                \
			"\tAdditional Buckets memory overhead: %.2lf%%\n",     \
			100.0 * (double)(map_ptr)->mctx.balloc_size /          \
				(__bucket_size *                               \
				 (1ull << (map_ptr)->buckets_exp)));           \
		fprintf(__file,                                                \
			"\tNumber of Buckers per Chunk: %llu\n",               \
			(1ull << (map_ptr)->buckets_per_chunk_exp));           \
		size_t __touched_counts[1 + __TTLMAP_BUCKET_ENTRIES];          \
		memset(__touched_counts, 0, sizeof(__touched_counts));         \
		for (size_t __i = 0; __i < (1ull << (map_ptr)->buckets_exp);   \
		     ++__i) {                                                  \
			size_t __elems = __TTLMAP_BUCKET_ELEMENTS_TOUCHED(     \
				map_ptr, __i, key_type, value_type             \
			);                                                     \
			++__touched_counts[__elems];                           \
		}                                                              \
		fprintf(__file,                                                \
			"\tNumber of Buckers per Number of touched elements "  \
			"(0-%u): [",                                           \
			__TTLMAP_BUCKET_ENTRIES);                              \
		for (size_t __i = 0; __i <= __TTLMAP_BUCKET_ENTRIES; ++__i) {  \
			fprintf(__file, "%zu", __touched_counts[__i]);         \
			if (__i < __TTLMAP_BUCKET_ENTRIES) {                   \
				fprintf(__file, ", ");                         \
			} else {                                               \
				fprintf(__file, "]\n");                        \
			}                                                      \
		}                                                              \
		fprintf(__file, "\tChunk sizes: [");                           \
		for (size_t __i = 0; __i < __TTLMAP_MAX_CHUNKS; ++__i) {       \
			if (__i == 0 || __i + 1 == __TTLMAP_MAX_CHUNKS ||      \
			    (map_ptr)->chunk_sizes[__i] !=                     \
				    (map_ptr)->chunk_sizes[__i - 1]) {         \
				fprintf(__file,                                \
					"%zu",                                 \
					(map_ptr)->chunk_sizes[__i]);          \
				if (__i + 1 < __TTLMAP_MAX_CHUNKS) {           \
					fprintf(__file, ", ");                 \
				}                                              \
			}                                                      \
			if (__i + 1 == __TTLMAP_MAX_CHUNKS) {                  \
				fprintf(__file, "]\n");                        \
			}                                                      \
		}                                                              \
		fflush(__file);                                                \
	})
