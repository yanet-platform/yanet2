
#pragma once

#include "big_array.h"
#include <assert.h>
#include <immintrin.h>
#include <stdio.h>
#include <string.h>

struct btree_block {
	uint8_t bytes[64];
} __attribute__((aligned(64)));

struct btree {
	struct big_array array;
	size_t n;
	size_t h;
	size_t max_h_cnt;
};

////////////////////////////////////////////////////////////////////////////////
// Helper functions
////////////////////////////////////////////////////////////////////////////////

#define typeof_eq(x, type) _Generic((x), type: 1, default: 0)

int
__get_gte_mask_avx2(__m256i target, const uint32_t *data) { // NOLINT
	__m256i vec = _mm256_load_si256((__m256i *)data);

	__m256i max_val = _mm256_max_epu32(vec, target);
	__m256i mask = _mm256_cmpeq_epi32(vec, max_val);

	return _mm256_movemask_ps((__m256)mask);
}

int
__get_gte_mask_avx2_64(
	__m256i target, __m256i sign_bit, const uint64_t *data
) { // NOLINT
	__m256i vec = _mm256_load_si256((__m256i *)data);

	// Flip sign bit for unsigned->signed comparison
	__m256i vec_signed = _mm256_xor_si256(vec, sign_bit);
	__m256i target_signed = _mm256_xor_si256(target, sign_bit);

	__m256i lt_mask = _mm256_cmpgt_epi64(target_signed, vec_signed);
	__m256i gte_mask = _mm256_xor_si256(lt_mask, _mm256_set1_epi32(-1));

	return _mm256_movemask_pd(_mm256_castsi256_pd(gte_mask));
}

static inline size_t
__btree_nblocks(struct btree *btree) { // NOLINT
	return btree->array.size / sizeof(struct btree_block);
}

static inline size_t
__btree_next(size_t v, size_t b, size_t i) { // NOLINT
	return v * (b + 1) + i + 1;
}

static inline size_t
__btree_b(size_t elem_size) { // NOLINT
	return 64 / elem_size;
}

void
__btree_build( // NOLINT
	struct btree *btree,
	size_t v,
	const void *a,
	size_t *t,
	size_t n,
	size_t elem_size,
	size_t h
) {
	if (v >= __btree_nblocks(btree)) {
		return;
	}
	if (btree->h < h) {
		btree->h = h;
		btree->max_h_cnt = 0;
	}
	const size_t b = __btree_b(elem_size);
	for (size_t i = 0; i < b; ++i) {
		size_t next = __btree_next(v, b, i);
		__btree_build(btree, next, a, t, n, elem_size, h + 1);
		if ((*t) < n) {
			const void *cur = a + (*t) * elem_size;
			void *dst = big_array_get(
				&btree->array,
				v * sizeof(struct btree_block) + i * elem_size
			);
			memcpy(dst, cur, elem_size);
			if (btree->h == h) {
				++btree->max_h_cnt;
			}
			(*t)++;
		}
	}
	size_t next = __btree_next(v, b, b);
	__btree_build(btree, next, a, t, n, elem_size, h + 1);
}

////////////////////////////////////////////////////////////////////////////////

#define BTREE_INIT(btree_ptr, a, size, mctx)                                   \
	__extension__({                                                        \
		__label__ __done;                                              \
		(btree_ptr)->n = (size);                                       \
		(btree_ptr)->h = 0;                                            \
		(btree_ptr)->max_h_cnt = 0;                                    \
		const size_t __b = __btree_b(sizeof((a)[0]));                  \
		size_t __nblocks = ((size) + __b - 1) / __b;                   \
		size_t __bytes = __nblocks * sizeof(struct btree_block);       \
		int __ret = 0;                                                 \
		if (big_array_init(&(btree_ptr)->array, __bytes, mctx) != 0) { \
			__ret = -1;                                            \
			goto __done;                                           \
		}                                                              \
		for (size_t __i = 0; __i < __bytes / sizeof((a)[0]); ++__i) {  \
			memset(big_array_get(                                  \
				       &(btree_ptr)->array,                    \
				       __i * sizeof((a)[0])                    \
			       ),                                              \
			       (a)[(size) - 1],                                \
			       sizeof((a)[0]));                                \
		}                                                              \
		size_t __idx = 0;                                              \
		__btree_build(                                                 \
			btree_ptr, 0, (a), &__idx, (size), sizeof((a)[0]), 0   \
		);                                                             \
	__done:                                                                \
		__ret;                                                         \
	})

#define BTREE_FREE(btree_ptr) big_array_free(&(btree_ptr)->array)

#define __BTREE_BLOCK_SEACH(                                                   \
	btree_block, y, simd_target, simd_target64, simd_signbit64             \
)                                                                              \
	__extension__({                                                        \
		typeof((y)) __y = (y);                                         \
		const typeof(&__y) __bytes =                                   \
			(const typeof(&__y))((btree_block)->bytes);            \
		size_t __b = __btree_b(sizeof(__y));                           \
		unsigned long long __mask = (1ull << __b);                     \
		if (typeof_eq(__y, uint32_t)) {                                \
			__mask |=                                              \
				__get_gte_mask_avx2(                           \
					simd_target, (uint32_t *)__bytes       \
				) |                                            \
				(__get_gte_mask_avx2(                          \
					 simd_target, (uint32_t *)__bytes + 8  \
				 )                                             \
				 << 8);                                        \
		} else if (typeof_eq(__y, uint64_t)) {                         \
			__mask |= __get_gte_mask_avx2_64(                      \
					  simd_target64,                       \
					  simd_signbit64,                      \
					  (uint64_t *)__bytes                  \
				  ) |                                          \
				  (__get_gte_mask_avx2_64(                     \
					   simd_target64,                      \
					   simd_signbit64,                     \
					   (uint64_t *)__bytes + 4             \
				   )                                           \
				   << 4);                                      \
		} else {                                                       \
			for (size_t __i = 0; __i < __b; ++__i) {               \
				__mask |=                                      \
					((unsigned long long)(__bytes[__i] >=  \
							      __y))            \
					<< __i;                                \
			}                                                      \
		}                                                              \
		__builtin_ffsll(__mask) - 1;                                   \
	})

#define BTREE_LOWER_BOUND(btree_ptr, x)                                        \
	__extension__({                                                        \
		typeof((x)) __x = (x);                                         \
		const size_t __nblocks = __btree_nblocks(btree_ptr);           \
		const size_t __b = __btree_b(sizeof(__x));                     \
		size_t __res = 0;                                              \
		size_t __k = 0;                                                \
		size_t __steps = 0;                                            \
		__m256i __simd_x = _mm256_set1_epi32(__x);                     \
		__m256i __simd_x64 = _mm256_set1_epi64x(__x);                  \
		__m256i sign_bit = _mm256_set1_epi64x(0x8000000000000000ULL);  \
		while (__k < __nblocks) {                                      \
			++__steps;                                             \
			size_t __i = __BTREE_BLOCK_SEACH(                      \
				(struct btree_block *)big_array_get(           \
					&(btree_ptr)->array,                   \
					__k * sizeof(struct btree_block)       \
				),                                             \
				__x,                                           \
				__simd_x,                                      \
				__simd_x64,                                    \
				sign_bit                                       \
			);                                                     \
			__res *= __b + 1;                                      \
			__res += __i;                                          \
			size_t __next = __btree_next(__k, __b, __i);           \
			__k = __next;                                          \
		}                                                              \
		if (__steps <= (btree_ptr)->h) {                               \
			__res += (btree_ptr)->max_h_cnt;                       \
		}                                                              \
		(__res < ((btree_ptr)->n) ? __res : (btree_ptr)->n);           \
	})

#define BTREE_UPPER_BOUND(btree_ptr, x)                                        \
	BTREE_LOWER_BOUND(btree_ptr, (typeof(x))((x) + 1))
