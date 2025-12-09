#pragma once

#include "key_value.h"

////////////////////////////////////////////////////////////////////////////////

#define TTLMAP_FOUND (0b01)
#define TTLMAP_INSERTED (0b10)
#define TTLMAP_REPLACED (0b11)
#define TTLMAP_FAILED (0b00)
#define TTLMAP_STATUS_MASK (0b11)
#define TTLMAP_STATUS_BITS (2)

////////////////////////////////////////////////////////////////////////////////

#define TTLMAP_STATUS(op_result) ((op_result) & TTLMAP_STATUS_MASK)
#define TTLMAP_META(op_result) ((uint32_t)((op_result) >> TTLMAP_STATUS_BITS))

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_BUCKET_ENTRIES_EXP 4
#define __TTLMAP_BUCKET_ENTRIES (1 << __TTLMAP_BUCKET_ENTRIES_EXP)

#define __TTLMAP_BUCKET_ENTRY_DECLARE(key_type, value_type)                    \
	typedef struct {                                                       \
		key_type key;                                                  \
		value_type value;                                              \
		uint32_t deadline;                                             \
	} __bucket_entry_t

#define __TTLMAP_BUCKET_DECLARE(key_type, value_type)                          \
	__TTLMAP_BUCKET_ENTRY_DECLARE(key_type, value_type);                   \
	typedef struct __bucket {                                              \
		__bucket_entry_t entries[__TTLMAP_BUCKET_ENTRIES];             \
		ttlmap_lock_t lock;                                            \
	} __attribute__((__aligned__(64))) __bucket_t

#define __TTLMAP_BUCKET_INIT(bucket_ptr, key_type, value_type)                 \
	__extension__({                                                        \
		__TTLMAP_BUCKET_DECLARE(key_type, value_type);                 \
		__bucket_t *__bucket = (__bucket_t *)bucket_ptr;               \
		for (size_t i = 0; i < __TTLMAP_BUCKET_ENTRIES; ++i) {         \
			__bucket->entries[i].deadline = 0;                     \
		}                                                              \
		__ttlmap_lock_init(&__bucket->lock);                           \
	})

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_BUCKET_LOOKUP(bucket_ptr, key_ptr, value_ptr, now, idx)       \
	;                                                                      \
	__extension__({                                                        \
		__label__ __done;                                              \
		int __ret = TTLMAP_FAILED;                                     \
		typedef typeof(*(key_ptr)) __key_type;                         \
		typedef typeof(*(value_ptr)) __value_type;                     \
		__TTLMAP_BUCKET_DECLARE(__key_type, __value_type);             \
		__bucket_t *__bucket = (__bucket_t *)(bucket_ptr);             \
		__ttlmap_lock(&__bucket->lock);                                \
		for (size_t __i = 0; __i < __TTLMAP_BUCKET_ENTRIES; ++__i) {   \
			size_t __pos =                                         \
				(__i + (idx)) & (__TTLMAP_BUCKET_ENTRIES - 1); \
			__bucket_entry_t *entry = &__bucket->entries[__pos];   \
			if (entry->deadline > (now) &&                         \
			    __TTLMAP_KEYS_EQUAL((key_ptr), &entry->key)) {     \
				memcpy((value_ptr),                            \
				       &entry->value,                          \
				       sizeof(__value_type));                  \
				__ret = (__i << (TTLMAP_STATUS_BITS)) |        \
					TTLMAP_FOUND;                          \
				goto __done;                                   \
			}                                                      \
		}                                                              \
	__done:                                                                \
		__ttlmap_unlock(&__bucket->lock);                              \
		__ret;                                                         \
	})

#define __TTLMAP_BUCKET_GET(                                                   \
	bucket_ptr, key_ptr, value_ptr_ptr, lock_ptr_ptr, now, timeout, idx    \
)                                                                              \
	__extension__({                                                        \
		__label__ __done;                                              \
		int __ret = TTLMAP_FAILED;                                     \
		typedef typeof(*(key_ptr)) __key_type;                         \
		typedef typeof(**(value_ptr_ptr)) __value_type;                \
		__TTLMAP_BUCKET_DECLARE(__key_type, __value_type);             \
		__bucket_t *__bucket = (__bucket_t *)(bucket_ptr);             \
		*(lock_ptr_ptr) = &__bucket->lock;                             \
		__ttlmap_lock(&__bucket->lock);                                \
		for (size_t __i = 0; __i < __TTLMAP_BUCKET_ENTRIES; ++__i) {   \
			size_t __pos =                                         \
				(__i + (idx)) & (__TTLMAP_BUCKET_ENTRIES - 1); \
			__bucket_entry_t *entry = &__bucket->entries[__pos];   \
			if (entry->deadline > (now) &&                         \
			    __TTLMAP_KEYS_EQUAL((key_ptr), &entry->key)) {     \
				entry->deadline = (now) + (timeout);           \
				*(value_ptr_ptr) = &entry->value;              \
				__ret = (__i << TTLMAP_STATUS_BITS) |          \
					TTLMAP_FOUND;                          \
				goto __done;                                   \
			}                                                      \
		}                                                              \
		for (size_t __i = 0; __i < __TTLMAP_BUCKET_ENTRIES; ++__i) {   \
			size_t __pos =                                         \
				(__i + (idx)) & (__TTLMAP_BUCKET_ENTRIES - 1); \
			__bucket_entry_t *entry = &__bucket->entries[__pos];   \
			if (entry->deadline <= (now)) {                        \
				__ret = (__i << TTLMAP_STATUS_BITS) |          \
					((entry->deadline > 0)                 \
						 ? TTLMAP_REPLACED             \
						 : TTLMAP_INSERTED);           \
				entry->deadline = (now) + (timeout);           \
				__TTLMAP_MEMORY_SET(&entry->key, (key_ptr));   \
				*(value_ptr_ptr) = &entry->value;              \
				goto __done;                                   \
			}                                                      \
		}                                                              \
		/* failed */                                                   \
		__ttlmap_unlock(&__bucket->lock);                              \
	__done:                                                                \
		__ret;                                                         \
	})

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_INVALIDATE_INTERNAL(key_type, value_ptr)                      \
	__extension__({                                                        \
		typedef typeof(*value_ptr) __value_type;                       \
		__TTLMAP_BUCKET_ENTRY_DECLARE(key_type, __value_type);         \
		__bucket_entry_t *__entry =                                    \
			container_of((value_ptr), __bucket_entry_t, value);    \
		__entry->deadline = 0;                                         \
	});

////////////////////////////////////////////////////////////////////////////////

static inline size_t
__ttlmap_bucket_count(size_t kv_entries) { // NOLINT
	size_t buckets = (kv_entries + __TTLMAP_BUCKET_ENTRIES - 1) /
			 __TTLMAP_BUCKET_ENTRIES;
	size_t max_bit = 63 - __builtin_clzll(buckets);
	if (buckets != (1ull << max_bit)) {
		++max_bit;
	}
	size_t res = 1ull << max_bit;
	return res;
}

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_BUCKET_FIND_WITH_ID(map_ptr, bucket_id, key_type, value_type) \
	__extension__({                                                        \
		__TTLMAP_BUCKET_DECLARE(key_type, value_type);                 \
		uint32_t __bucket = (bucket_id);                               \
		uint32_t __chunk =                                             \
			__bucket >> ((map_ptr)->buckets_per_chunk_exp);        \
		uint32_t __buckets_per_chunk =                                 \
			1 << ((map_ptr)->buckets_per_chunk_exp);               \
		uint32_t __bucket_in_chunk =                                   \
			__bucket & (__buckets_per_chunk - 1);                  \
		__bucket_t *__buckets_array =                                  \
			ADDR_OF(&((map_ptr)->chunks[__chunk]));                \
		(void *)&__buckets_array[__bucket_in_chunk];                   \
	})

#define __TTLMAP_BUCKET_FIND(map_ptr, key_ptr, value_type)                     \
	__extension__({                                                        \
		uint32_t __hash = __TTLMAP_KEY_HASH((key_ptr));                \
		uint32_t __buckets = 1 << ((map_ptr)->buckets_exp);            \
		uint32_t __bucket_id = __hash & (__buckets - 1);               \
		__TTLMAP_BUCKET_FIND_WITH_ID(                                  \
			map_ptr, __bucket_id, typeof(*(key_ptr)), value_type   \
		);                                                             \
	})

////////////////////////////////////////////////////////////////////////////////

#define __TTLMAP_BUCKET_ELEMENTS_TOUCHED(                                      \
	map_ptr, bucket_id, key_type, value_type                               \
)                                                                              \
	__extension__({                                                        \
		const void *__addr = __TTLMAP_BUCKET_FIND_WITH_ID(             \
			map_ptr, bucket_id, key_type, value_type               \
		);                                                             \
		__TTLMAP_BUCKET_DECLARE(key_type, value_type);                 \
		const __bucket_t *__bucket = (const __bucket_t *)__addr;       \
		size_t __count = 0;                                            \
		for (size_t __i = 0; __i < __TTLMAP_BUCKET_ENTRIES; ++__i) {   \
			if (__bucket->entries[__i].deadline > 0) {             \
				++__count;                                     \
			}                                                      \
		}                                                              \
		__count;                                                       \
	})

#define __TTLMAP_BUCKET_ITER(                                                  \
	map_ptr, bucket_id, key_type, value_type, now, cb, data                \
)                                                                              \
	__extension__({                                                        \
		int __result = 0;                                              \
		void *__addr = __TTLMAP_BUCKET_FIND_WITH_ID(                   \
			map_ptr, bucket_id, key_type, value_type               \
		);                                                             \
		__TTLMAP_BUCKET_DECLARE(key_type, value_type);                 \
		__bucket_t *__bucket = (__bucket_t *)__addr;                   \
		__ttlmap_lock(&__bucket->lock);                                \
		for (size_t __i = 0; __i < __TTLMAP_BUCKET_ENTRIES; ++__i) {   \
			if (__bucket->entries[__i].deadline > (now)) {         \
				if ((cb)(&__bucket->entries[__i].key,          \
					 &__bucket->entries[__i].value,        \
					 (data))) {                            \
					__ttlmap_unlock(&__bucket->lock);      \
					__result = 1;                          \
					break;                                 \
				}                                              \
			}                                                      \
		}                                                              \
		__ttlmap_unlock(&__bucket->lock);                              \
		__result;                                                      \
	})
