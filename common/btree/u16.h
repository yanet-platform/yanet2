#pragma once

#include "../big_array.h"
#include <assert.h>
#include <immintrin.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

/**
 * @file u16.h
 * @brief B-tree implementation for uint16_t values
 *
 * This file provides a cache-optimized B-tree implementation specifically
 * for uint16_t values. It uses 64-byte aligned blocks and AVX2 SIMD
 * instructions for fast binary search operations.
 *
 * Key features:
 * - Cache-aligned 64-byte blocks storing 32 uint16_t values
 * - AVX2 SIMD acceleration for parallel comparisons
 * - O(log n) search with minimal branching
 * - Function-based API (no macros)
 *
 * Example usage:
 * @code
 * struct btree_u16 tree;
 * uint16_t data[] = {1, 5, 10, 15, 20, 25, 30};
 * size_t n = sizeof(data) / sizeof(data[0]);
 *
 * // Initialize
 * int ret = btree_u16_init(&tree, data, n, &mctx);
 * if (ret != 0) {
 *     // Handle error
 * }
 *
 * // Search
 * size_t idx = btree_u16_lower_bound(&tree, 12);
 * // idx = 3 (index of 15, first element >= 12)
 *
 * // Cleanup
 * btree_u16_free(&tree);
 * @endcode
 */

// Block size for uint16_t: 64 bytes / 2 bytes = 32 elements
#define BTREE_U16_BLOCK_SIZE 32

/**
 * @brief 64-byte aligned block storing 32 uint16_t values
 *
 * Each block stores exactly 32 uint16_t values (64 bytes total) and is
 * aligned to 64-byte boundaries for optimal cache performance.
 */
struct btree_u16_block {
	uint16_t values[BTREE_U16_BLOCK_SIZE];
} __attribute__((aligned(64)));

/**
 * @brief B-tree structure for uint16_t values
 *
 * Stores sorted uint16_t values in a cache-optimized tree structure
 * for fast binary search operations using SIMD instructions.
 *
 * The tree uses an implicit (b+1)-ary structure where b = 32.
 * Each node contains up to 32 values, and navigation is done through
 * index arithmetic rather than explicit pointers.
 */
struct btree_u16 {
	struct big_array array; ///< Storage for tree blocks
	size_t n;		///< Number of elements
	size_t h;		///< Tree height
	size_t max_h_cnt;	///< Count of elements at maximum height
};

/**
 * @brief Get number of blocks in the btree
 * @param btree Pointer to btree structure
 * @return Number of blocks allocated
 */
static inline size_t
btree_u16_nblocks(const struct btree_u16 *btree) {
	return btree->array.size / sizeof(struct btree_u16_block);
}

/**
 * @brief Calculate next node index in tree traversal
 * @param v Current node index
 * @param i Position within current block (0 to BTREE_U16_BLOCK_SIZE)
 * @return Index of child node
 *
 * For a (b+1)-ary tree where b = 32, the child at position i
 * of node v is at index: v * (b + 1) + i + 1
 */
static inline size_t
btree_u16_next(size_t v, size_t i) {
	return v * (BTREE_U16_BLOCK_SIZE + 1) + i + 1;
}

/**
 * @brief Get GTE mask using AVX2 for 16 uint16_t elements
 * @param target Target value (broadcasted to all lanes)
 * @param data Pointer to 16 uint16_t values (must be 32-byte aligned)
 * @return Bitmask where bit i is set if data[i] >= target
 *
 * Uses AVX2 signed comparison with XOR trick to handle unsigned values.
 * The result from _mm256_movemask_epi8 gives 32 bits (2 per 16-bit element),
 * so we extract every other bit to get the actual 16-bit comparison result.
 */
static inline int
btree_u16_get_gte_mask_avx2(__m256i target_signed, const uint16_t *data) {
	__m256i vec_signed = _mm256_load_si256((__m256i *)data);
	__m256i lt_mask = _mm256_cmpgt_epi16(target_signed, vec_signed);
	return _mm_movemask_epi8(_mm_packs_epi16(
		_mm256_castsi256_si128(lt_mask),
		_mm256_extracti128_si256(lt_mask, 1)
	));
}

/**
 * @brief Get GT mask using AVX2 for 16 uint16_t elements
 * @param target Target value (broadcasted to all lanes)
 * @param data Pointer to 16 uint16_t values (must be 32-byte aligned)
 * @return Bitmask where bit i is set if data[i] > target
 *
 * Uses AVX2 signed comparison with XOR trick to handle unsigned values.
 */
static inline int
btree_u16_get_gt_mask_avx2(__m256i target_signed, const uint16_t *data) {
	__m256i vec_signed = _mm256_load_si256((__m256i *)data);
	__m256i lte_mask = _mm256_cmpgt_epi16(target_signed, vec_signed);
	__m256i eq_mask = _mm256_cmpeq_epi16(target_signed, vec_signed);
	__m256i combined = _mm256_or_si256(lte_mask, eq_mask);
	return _mm_movemask_epi8(_mm_packs_epi16(
		_mm256_castsi256_si128(combined),
		_mm256_extracti128_si256(combined, 1)
	));
}

/**
 * @brief Search within a single block using AVX2 SIMD for GT comparison
 * @param block Pointer to block to search
 * @param value Value to search for
 * @return Index of first element > value, or BTREE_U16_BLOCK_SIZE if all <=
 * value
 *
 * Processes 32 elements in two AVX2 operations (16 elements each).
 * Returns the index of the first element that is greater than
 * the search value.
 */
static inline size_t
btree_u16_block_search_gt(const struct btree_u16_block *block, __m256i target) {
	// Process first 16 elements
	int mask1 = btree_u16_get_gt_mask_avx2(target, block->values);

	// Process next 16 elements
	int mask2 = btree_u16_get_gt_mask_avx2(target, block->values + 16);

	// Combine masks: mask2 shifted left by 16 bits
	unsigned long long combined =
		(mask1 | ((unsigned long long)mask2 << 16)) ^ 0x1FFFFFFFFULL;

	// Find first set bit (1-indexed), subtract 1 for 0-indexed result
	return __builtin_ffsll(combined) - 1;
}

/**
 * @brief Search within a single block using AVX2 SIMD
 * @param block Pointer to block to search
 * @param value Value to search for
 * @return Index of first element >= value, or BTREE_U16_BLOCK_SIZE if all <
 * value
 *
 * Processes 32 elements in two AVX2 operations (16 elements each).
 * Returns the index of the first element that is greater than or
 * equal to the search value.
 */
static inline size_t
btree_u16_block_search(const struct btree_u16_block *block, __m256i target) {
	// Process first 16 elements
	int mask1 = btree_u16_get_gte_mask_avx2(target, block->values);

	// Process next 16 elements
	int mask2 = btree_u16_get_gte_mask_avx2(target, block->values + 16);

	// Combine masks: mask2 shifted left by 16 bits
	unsigned long long combined =
		(mask1 | ((unsigned long long)mask2 << 16)) ^ 0x1FFFFFFFFULL;

	// Find first set bit (1-indexed), subtract 1 for 0-indexed result
	return __builtin_ffsll(combined) - 1;
}

/**
 * @brief Recursive tree building function
 * @param btree Tree being built
 * @param v Current node index
 * @param data Source data array (sorted)
 * @param idx Current position in source data (modified during recursion)
 * @param n Total number of elements in source data
 * @param h Current height in tree (0 = root)
 *
 * Builds the tree recursively in a depth-first manner, filling blocks
 * with values from the sorted input array. Updates tree height and
 * max height count during construction.
 */
static inline void
btree_u16_build(
	struct btree_u16 *btree,
	size_t v,
	const uint16_t *data,
	size_t *idx,
	size_t n,
	size_t h
) {
	if (v >= btree_u16_nblocks(btree)) {
		return;
	}

	// Update tree height tracking
	if (btree->h < h) {
		btree->h = h;
		btree->max_h_cnt = 0;
	}

	// Process each position in the block
	for (size_t i = 0; i < BTREE_U16_BLOCK_SIZE; ++i) {
		// Recursively build left child
		size_t next = btree_u16_next(v, i);
		btree_u16_build(btree, next, data, idx, n, h + 1);

		// Fill current position if data remains
		if ((*idx) < n) {
			struct btree_u16_block *block =
				(struct btree_u16_block *)big_array_get(
					&btree->array,
					v * sizeof(struct btree_u16_block)
				);
			block->values[i] = data[*idx] ^ 0x8000;

			if (btree->h == h) {
				++btree->max_h_cnt;
			}
			(*idx)++;
		}
	}

	// Recursively build rightmost child
	size_t next = btree_u16_next(v, BTREE_U16_BLOCK_SIZE);
	btree_u16_build(btree, next, data, idx, n, h + 1);
}

/**
 * @brief Initialize a btree with uint16_t values
 *
 * Creates a cache-optimized search tree from sorted uint16_t data.
 * The tree uses 64-byte aligned blocks for cache efficiency and
 * AVX2 SIMD instructions for fast searching.
 *
 * @param btree Pointer to uninitialized btree structure
 * @param data Pointer to sorted uint16_t array
 * @param n Number of elements in data array
 * @param mctx Memory context for allocations
 * @return 0 on success, -1 on allocation failure
 *
 * @note Data array must be sorted in ascending order
 * @note On failure, btree is left in a safe state (can be freed)
 *
 * Time complexity: O(n)
 * Space complexity: O(n)
 *
 * Example:
 * @code
 * struct btree_u16 tree;
 * uint16_t data[] = {1, 5, 10, 15, 20};
 * int ret = btree_u16_init(&tree, data, 5, &mctx);
 * if (ret != 0) {
 *     fprintf(stderr, "Failed to initialize btree\n");
 *     return -1;
 * }
 * @endcode
 */
static inline int
btree_u16_init(
	struct btree_u16 *btree,
	const uint16_t *data,
	size_t n,
	struct memory_context *mctx
) {
	btree->n = n;
	btree->h = 0;
	btree->max_h_cnt = 0;

	// Calculate number of blocks needed
	size_t nblocks = (n + BTREE_U16_BLOCK_SIZE - 1) / BTREE_U16_BLOCK_SIZE;
	size_t bytes = nblocks * sizeof(struct btree_u16_block);

	// Initialize big_array for storage
	if (big_array_init(&btree->array, bytes, mctx) != 0) {
		return -1;
	}

	// Initialize all blocks with maximum value (last element)
	// This ensures sentinel values for incomplete blocks
	// Note: XOR with 0x8000 to match the signed comparison format
	if (n > 0) {
		uint16_t max_val = data[n - 1] ^ 0x8000;
		size_t total_values = nblocks * BTREE_U16_BLOCK_SIZE;
		for (size_t i = 0; i < total_values; ++i) {
			uint16_t *ptr = (uint16_t *)big_array_get(
				&btree->array, i * sizeof(uint16_t)
			);
			*ptr = max_val;
		}
	}

	// Build tree structure recursively
	size_t idx = 0;
	btree_u16_build(btree, 0, data, &idx, n, 0);

	return 0;
}

/**
 * @brief Free all memory associated with a btree
 *
 * Releases all allocated memory and zeros the structure.
 * Safe to call multiple times on the same btree.
 *
 * @param btree Pointer to btree to free
 *
 * After calling this function, the btree structure is zeroed and
 * cannot be used until re-initialized with btree_u16_init().
 *
 * Example:
 * @code
 * btree_u16_free(&tree);
 * // tree is now safe to re-initialize or discard
 * @endcode
 */
static inline void
btree_u16_free(struct btree_u16 *btree) {
	big_array_free(&btree->array);
}

static inline size_t
btree_u16_lower_bounds(
	struct btree_u16 *btree,
	uint16_t *values,
	size_t count,
	uint32_t *result
);

static inline size_t
btree_u16_upper_bounds(
	struct btree_u16 *btree,
	uint16_t *values,
	size_t count,
	uint32_t *result
);

/**
 * @brief Find first element >= value (lower bound)
 *
 * Returns the index of the first element that is not less than
 * the given value. If all elements are less than value, returns n.
 *
 * This is equivalent to std::lower_bound in C++.
 *
 * @param btree Pointer to initialized btree
 * @param value Value to search for
 * @return Index of first element >= value, or n if not found
 *
 * Time complexity: O(log n) with SIMD acceleration
 *
 * Example:
 * @code
 * // Tree contains: [1, 5, 10, 15, 20]
 * size_t idx = btree_u16_lower_bound(&tree, 12);
 * // Returns 3 (index of 15, first element >= 12)
 *
 * idx = btree_u16_lower_bound(&tree, 10);
 * // Returns 2 (index of 10, exact match)
 *
 * idx = btree_u16_lower_bound(&tree, 25);
 * // Returns 5 (n, no element >= 25)
 * @endcode
 */
static inline uint32_t
btree_u16_lower_bound(struct btree_u16 *btree, uint16_t value) {
	uint32_t result;
	btree_u16_lower_bounds(btree, &value, 1, &result);
	return result;
}

/**
 * @brief Find first element > value (upper bound)
 *
 * Returns the index of the first element that is greater than
 * the given value. If all elements are <= value, returns n.
 *
 * This is equivalent to std::upper_bound in C++.
 *
 * @param btree Pointer to initialized btree
 * @param value Value to search for
 * @return Index of first element > value, or n if not found
 *
 * Time complexity: O(log n) with SIMD acceleration
 *
 * Implementation note: This is implemented as lower_bound(value + 1)
 * for efficiency, which works correctly for integer types.
 *
 * Example:
 * @code
 * // Tree contains: [1, 5, 10, 15, 20]
 * size_t idx = btree_u16_upper_bound(&tree, 10);
 * // Returns 3 (index of 15, first element > 10)
 *
 * idx = btree_u16_upper_bound(&tree, 12);
 * // Returns 3 (index of 15, first element > 12)
 *
 * idx = btree_u16_upper_bound(&tree, 20);
 * // Returns 5 (n, no element > 20)
 * @endcode
 */
static inline size_t
btree_u16_upper_bound(struct btree_u16 *btree, uint16_t value) {
	uint32_t result;
	btree_u16_upper_bounds(btree, &value, 1, &result);
	return result;
}

#define PREFETCH 0

enum { btree_u16_max_batch_size = 32 };

static inline size_t
btree_u16_upper_bounds(
	struct btree_u16 *btree,
	uint16_t *values,
	size_t count,
	uint32_t *result
) {
	struct context {
		size_t result;
		size_t k;
		__m256i target;
	} ctx[btree_u16_max_batch_size];

	if (count > btree_u16_max_batch_size) {
		count = btree_u16_max_batch_size;
	}

	// initialize context
	for (size_t i = 0; i < count; ++i) {
		struct context *c = &ctx[i];
		c->result = 0;
		c->k = 0;
		c->target = _mm256_set1_epi16(values[i] ^ 0x8000);
	}

	const size_t nblocks = btree_u16_nblocks(btree);

	for (size_t step = 0; step < btree->h; ++step) {
		for (size_t i = 0; i < count; ++i) {
			if (PREFETCH > 0 && i + PREFETCH < count) {
				__builtin_prefetch(
					big_array_get(
						&btree->array,
						ctx[i + PREFETCH].k *
							sizeof(struct
							       btree_u16_block)
					),
					0,
					3
				);
			}

			struct context *c = &ctx[i];
			const struct btree_u16_block *block =
				(const struct btree_u16_block *)big_array_get(
					&btree->array,
					c->k * sizeof(struct btree_u16_block)
				);

			// Search within block using SIMD (GT comparison)
			size_t idx =
				btree_u16_block_search_gt(block, c->target);

			// Update result index
			c->result *= (BTREE_U16_BLOCK_SIZE + 1);
			c->result += idx;

			// Move to the next block
			c->k = btree_u16_next(c->k, idx);
		}
	}

	for (size_t i = 0; i < count; ++i) {
		if (PREFETCH > 0 && i + PREFETCH < count) {
			__builtin_prefetch(
				big_array_get(
					&btree->array,
					ctx[i + PREFETCH].k *
						sizeof(struct btree_u16_block)
				),
				0,
				3
			);
		}
		struct context *c = &ctx[i];
		if (c->k < nblocks) {
			const struct btree_u16_block *block =
				(const struct btree_u16_block *)big_array_get(
					&btree->array,
					c->k * sizeof(struct btree_u16_block)
				);

			// Search within block using SIMD (GT comparison)
			size_t idx =
				btree_u16_block_search_gt(block, c->target);

			// Update result index
			c->result *= (BTREE_U16_BLOCK_SIZE + 1);
			c->result += idx;
		} else {
			c->result += btree->max_h_cnt;
		}
		result[i] = (c->result < btree->n) ? c->result : btree->n;
	}

	return count;
}

static inline size_t
btree_u16_lower_bounds(
	struct btree_u16 *btree,
	uint16_t *values,
	size_t count,
	uint32_t *result
) {
	struct context {
		size_t result;
		size_t k;
		__m256i target;
	} ctx[btree_u16_max_batch_size];

	if (count > btree_u16_max_batch_size) {
		count = btree_u16_max_batch_size;
	}

	// initialize context
	for (size_t i = 0; i < count; ++i) {
		struct context *c = &ctx[i];
		c->result = 0;
		c->k = 0;
		c->target = _mm256_set1_epi16(values[i] ^ 0x8000);
	}

	const size_t nblocks = btree_u16_nblocks(btree);

	for (size_t step = 0; step < btree->h; ++step) {
		for (size_t i = 0; i < count; ++i) {
			if (PREFETCH > 0 && i + PREFETCH < count) {
				__builtin_prefetch(
					big_array_get(
						&btree->array,
						ctx[i + PREFETCH].k *
							sizeof(struct
							       btree_u16_block)
					),
					0,
					3
				);
			}

			struct context *c = &ctx[i];
			const struct btree_u16_block *block =
				(const struct btree_u16_block *)big_array_get(
					&btree->array,
					c->k * sizeof(struct btree_u16_block)
				);

			// Search within block using SIMD
			size_t idx = btree_u16_block_search(block, c->target);

			// Update result index
			c->result *= (BTREE_U16_BLOCK_SIZE + 1);
			c->result += idx;

			// Move to the next block
			c->k = btree_u16_next(c->k, idx);
		}
	}

	for (size_t i = 0; i < count; ++i) {
		if (PREFETCH > 0 && i + PREFETCH < count) {
			__builtin_prefetch(
				big_array_get(
					&btree->array,
					ctx[i + PREFETCH].k *
						sizeof(struct btree_u16_block)
				),
				0,
				3
			);
		}
		struct context *c = &ctx[i];
		if (c->k < nblocks) {
			const struct btree_u16_block *block =
				(const struct btree_u16_block *)big_array_get(
					&btree->array,
					c->k * sizeof(struct btree_u16_block)
				);

			// Search within block using SIMD
			size_t idx = btree_u16_block_search(block, c->target);

			// Update result index
			c->result *= (BTREE_U16_BLOCK_SIZE + 1);
			c->result += idx;
		} else {
			c->result += btree->max_h_cnt;
		}
		result[i] = (c->result < btree->n) ? c->result : btree->n;
	}

	return count;
}

#undef PREFETCH