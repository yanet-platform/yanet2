#pragma once

#include "../big_array.h"
#include <assert.h>
#include <immintrin.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

/**
 * @file u64.h
 * @brief B-tree implementation for uint64_t values
 *
 * This file provides a cache-optimized B-tree implementation specifically
 * for uint64_t values. It uses 64-byte aligned blocks and AVX2 SIMD
 * instructions for fast binary search operations.
 *
 * Key features:
 * - Cache-aligned 64-byte blocks storing 8 uint64_t values
 * - AVX2 SIMD acceleration for parallel comparisons
 * - O(log n) search with minimal branching
 * - Function-based API (no macros)
 *
 * Example usage:
 * @code
 * struct btree_u64 tree;
 * uint64_t data[] = {100, 500, 1000, 1500, 2000, 2500, 3000};
 * size_t n = sizeof(data) / sizeof(data[0]);
 *
 * // Initialize
 * int ret = btree_u64_init(&tree, data, n, &mctx);
 * if (ret != 0) {
 *     // Handle error
 * }
 *
 * // Search
 * size_t idx = btree_u64_lower_bound(&tree, 1200);
 * // idx = 3 (index of 1500, first element >= 1200)
 *
 * // Cleanup
 * btree_u64_free(&tree);
 * @endcode
 */

// Block size for uint64_t: 64 bytes / 8 bytes = 8 elements
#define BTREE_U64_BLOCK_SIZE 8

/**
 * @brief 64-byte aligned block storing 8 uint64_t values
 *
 * Each block stores exactly 8 uint64_t values (64 bytes total) and is
 * aligned to 64-byte boundaries for optimal cache performance.
 */
struct btree_u64_block {
	uint64_t values[BTREE_U64_BLOCK_SIZE];
} __attribute__((aligned(64)));

/**
 * @brief B-tree structure for uint64_t values
 *
 * Stores sorted uint64_t values in a cache-optimized tree structure
 * for fast binary search operations using SIMD instructions.
 *
 * The tree uses an implicit (b+1)-ary structure where b = 8.
 * Each node contains up to 8 values, and navigation is done through
 * index arithmetic rather than explicit pointers.
 */
struct btree_u64 {
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
btree_u64_nblocks(const struct btree_u64 *btree) {
	return btree->array.size / sizeof(struct btree_u64_block);
}

/**
 * @brief Calculate next node index in tree traversal
 * @param v Current node index
 * @param i Position within current block (0 to BTREE_U64_BLOCK_SIZE)
 * @return Index of child node
 *
 * For a (b+1)-ary tree where b = 8, the child at position i
 * of node v is at index: v * (b + 1) + i + 1
 */
static inline size_t
btree_u64_next(size_t v, size_t i) {
	return v * (BTREE_U64_BLOCK_SIZE + 1) + i + 1;
}

/**
 * @brief Get GTE mask using AVX2 for 4 uint64_t elements
 * @param target_signed Target value XORed with sign bit
 * @param sign_bit Sign bit mask for unsigned comparison trick
 * @param data Pointer to 4 uint64_t values (must be 32-byte aligned)
 * @return Bitmask where bit i is set if data[i] >= target
 *
 * Uses the signed comparison trick: XOR both operands with the sign bit
 * to convert unsigned comparison to signed comparison. This works because
 * XORing with 0x8000000000000000 flips the sign bit, making unsigned
 * values comparable using signed instructions.
 */
static inline int
btree_u64_get_gte_mask_avx2(__m256i target_signed, const uint64_t *data) {
	__m256i vec_signed = _mm256_load_si256((__m256i *)data);
	__m256i lt_mask = _mm256_cmpgt_epi64(target_signed, vec_signed);
	return _mm256_movemask_pd((__m256d)lt_mask);
}
/**
 * @brief Get GT mask using AVX2 for 4 uint64_t elements
 * @param target_signed Target value XORed with sign bit
 * @param data Pointer to 4 uint64_t values (must be 32-byte aligned)
 * @return Bitmask where bit i is set if data[i] > target
 *
 * Uses the signed comparison trick: XOR both operands with the sign bit
 * to convert unsigned comparison to signed comparison.
 */
static inline int
btree_u64_get_gt_mask_avx2(__m256i target_signed, const uint64_t *data) {
	__m256i vec_signed = _mm256_load_si256((__m256i *)data);
	__m256i lte_mask = _mm256_cmpgt_epi64(target_signed, vec_signed);
	__m256i eq_mask = _mm256_cmpeq_epi64(target_signed, vec_signed);
	__m256i combined = _mm256_or_si256(lte_mask, eq_mask);
	return _mm256_movemask_pd((__m256d)combined);
}

/**
 * @brief Search within a single block using AVX2 SIMD for GT comparison
 * @param block Pointer to block to search
 * @param target_signed Target value XORed with sign bit
 * @return Index of first element > value, or BTREE_U64_BLOCK_SIZE if all <=
 * value
 *
 * Processes 8 elements in two AVX2 operations (4 elements each).
 * Returns the index of the first element that is greater than
 * the search value.
 */
static inline size_t
btree_u64_block_search_gt(
	const struct btree_u64_block *block, __m256i target_signed
) {
	// Process first 4 elements
	int mask1 = btree_u64_get_gt_mask_avx2(target_signed, block->values);

	// Process next 4 elements
	int mask2 =
		btree_u64_get_gt_mask_avx2(target_signed, block->values + 4);

	// Combine masks: mask2 shifted left by 4 bits
	unsigned combined = (mask1 | (mask2 << 4)) ^ 0x1FF;

	// Find first set bit (1-indexed), subtract 1 for 0-indexed result
	return __builtin_ffs(combined) - 1;
}

/**
 * @brief Search within a single block using AVX2 SIMD
 * @param block Pointer to block to search
 * @param target_signed Target value XORed with sign bit
 * @param sign_bit Sign bit mask for unsigned comparison
 * @return Index of first element >= value, or BTREE_U64_BLOCK_SIZE if all <
 * value
 *
 * Processes 8 elements in two AVX2 operations (4 elements each).
 * Returns the index of the first element that is greater than or
 * equal to the search value.
 */
static inline size_t
btree_u64_block_search(
	const struct btree_u64_block *block, __m256i target_signed
) {
	// Process first 4 elements
	int mask1 = btree_u64_get_gte_mask_avx2(target_signed, block->values);

	// Process next 4 elements
	int mask2 =
		btree_u64_get_gte_mask_avx2(target_signed, block->values + 4);

	// Combine masks: mask2 shifted left by 4 bits
	unsigned combined = (mask1 | (mask2 << 4)) ^ 0x1FF;

	// Find first set bit (1-indexed), subtract 1 for 0-indexed result
	return __builtin_ffs(combined) - 1;
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
btree_u64_build(
	struct btree_u64 *btree,
	size_t v,
	const uint64_t *data,
	size_t *idx,
	size_t n,
	size_t h
) {
	if (v >= btree_u64_nblocks(btree)) {
		return;
	}

	// Update tree height tracking
	if (btree->h < h) {
		btree->h = h;
		btree->max_h_cnt = 0;
	}

	// Process each position in the block
	for (size_t i = 0; i < BTREE_U64_BLOCK_SIZE; ++i) {
		// Recursively build left child
		size_t next = btree_u64_next(v, i);
		btree_u64_build(btree, next, data, idx, n, h + 1);

		// Fill current position if data remains
		if ((*idx) < n) {
			struct btree_u64_block *block =
				(struct btree_u64_block *)big_array_get(
					&btree->array,
					v * sizeof(struct btree_u64_block)
				);
			block->values[i] = data[*idx] ^ 0x8000000000000000ULL;

			if (btree->h == h) {
				++btree->max_h_cnt;
			}
			(*idx)++;
		}
	}

	// Recursively build rightmost child
	size_t next = btree_u64_next(v, BTREE_U64_BLOCK_SIZE);
	btree_u64_build(btree, next, data, idx, n, h + 1);
}

/**
 * @brief Initialize a btree with uint64_t values
 *
 * Creates a cache-optimized search tree from sorted uint64_t data.
 * The tree uses 64-byte aligned blocks for cache efficiency and
 * AVX2 SIMD instructions for fast searching.
 *
 * @param btree Pointer to uninitialized btree structure
 * @param data Pointer to sorted uint64_t array
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
 * struct btree_u64 tree;
 * uint64_t data[] = {100, 500, 1000, 1500, 2000};
 * int ret = btree_u64_init(&tree, data, 5, &mctx);
 * if (ret != 0) {
 *     fprintf(stderr, "Failed to initialize btree\n");
 *     return -1;
 * }
 * @endcode
 */
static inline int
btree_u64_init(
	struct btree_u64 *btree,
	const uint64_t *data,
	size_t n,
	struct memory_context *mctx
) {
	btree->n = n;
	btree->h = 0;
	btree->max_h_cnt = 0;

	// Calculate number of blocks needed
	size_t nblocks = (n + BTREE_U64_BLOCK_SIZE - 1) / BTREE_U64_BLOCK_SIZE;
	size_t bytes = nblocks * sizeof(struct btree_u64_block);

	// Initialize big_array for storage
	if (big_array_init(&btree->array, bytes, mctx) != 0) {
		return -1;
	}

	// Initialize all blocks with maximum value (last element)
	// This ensures sentinel values for incomplete blocks
	if (n > 0) {
		uint64_t max_val = data[n - 1];
		size_t total_values = nblocks * BTREE_U64_BLOCK_SIZE;
		for (size_t i = 0; i < total_values; ++i) {
			uint64_t *ptr = (uint64_t *)big_array_get(
				&btree->array, i * sizeof(uint64_t)
			);
			*ptr = max_val;
		}
	}

	// Build tree structure recursively
	size_t idx = 0;
	btree_u64_build(btree, 0, data, &idx, n, 0);

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
 * cannot be used until re-initialized with btree_u64_init().
 *
 * Example:
 * @code
 * btree_u64_free(&tree);
 * // tree is now safe to re-initialize or discard
 * @endcode
 */
static inline void
btree_u64_free(struct btree_u64 *btree) {
	big_array_free(&btree->array);
}

static inline size_t
btree_u64_lower_bounds(
	struct btree_u64 *btree,
	uint64_t *values,
	size_t count,
	uint32_t *result
);

static inline size_t
btree_u64_upper_bounds(
	struct btree_u64 *btree,
	uint64_t *values,
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
 * // Tree contains: [100, 500, 1000, 1500, 2000]
 * size_t idx = btree_u64_lower_bound(&tree, 1200);
 * // Returns 3 (index of 1500, first element >= 1200)
 *
 * idx = btree_u64_lower_bound(&tree, 1000);
 * // Returns 2 (index of 1000, exact match)
 *
 * idx = btree_u64_lower_bound(&tree, 2500);
 * // Returns 5 (n, no element >= 2500)
 * @endcode
 */
static inline size_t
btree_u64_lower_bound(struct btree_u64 *btree, uint64_t value) {
	uint32_t result;
	btree_u64_lower_bounds(btree, &value, 1, &result);
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
 * // Tree contains: [100, 500, 1000, 1500, 2000]
 * size_t idx = btree_u64_upper_bound(&tree, 1000);
 * // Returns 3 (index of 1500, first element > 1000)
 *
 * idx = btree_u64_upper_bound(&tree, 1200);
 * // Returns 3 (index of 1500, first element > 1200)
 *
 * idx = btree_u64_upper_bound(&tree, 2000);
 * // Returns 5 (n, no element > 2000)
 * @endcode
 */
static inline size_t
btree_u64_upper_bound(struct btree_u64 *btree, uint64_t value) {
	uint32_t result;
	btree_u64_upper_bounds(btree, &value, 1, &result);
	return result;
}

#define PREFETCH 0

enum { btree_u64_max_batch_size = 32 };

static inline size_t
btree_u64_upper_bounds(
	struct btree_u64 *btree,
	uint64_t *values,
	size_t count,
	uint32_t *result
) {
	struct context {
		size_t result;
		size_t k;
		__m256i target;
	} ctx[btree_u64_max_batch_size];

	if (count > btree_u64_max_batch_size) {
		count = btree_u64_max_batch_size;
	}

	// initialize context
	for (size_t i = 0; i < count; ++i) {
		struct context *c = &ctx[i];
		c->result = 0;
		c->k = 0;
		c->target =
			_mm256_set1_epi64x(values[i] ^ 0x8000000000000000ULL);
		;
	}

	const size_t nblocks = btree_u64_nblocks(btree);

	for (size_t step = 0; step < btree->h; ++step) {
		for (size_t i = 0; i < count; ++i) {
			if (PREFETCH > 0 && i + PREFETCH < count) {
				__builtin_prefetch(
					big_array_get(
						&btree->array,
						ctx[i + PREFETCH].k *
							sizeof(struct
							       btree_u64_block)
					),
					0,
					3
				);
			}

			struct context *c = &ctx[i];
			const struct btree_u64_block *block =
				(const struct btree_u64_block *)big_array_get(
					&btree->array,
					c->k * sizeof(struct btree_u64_block)
				);

			// Search within block using SIMD (GT comparison)
			size_t idx =
				btree_u64_block_search_gt(block, c->target);

			// Update result index
			c->result *= (BTREE_U64_BLOCK_SIZE + 1);
			c->result += idx;

			// Move to the next block
			c->k = btree_u64_next(c->k, idx);
		}
	}

	for (size_t i = 0; i < count; ++i) {
		if (PREFETCH > 0 && i + PREFETCH < count) {
			__builtin_prefetch(
				big_array_get(
					&btree->array,
					ctx[i + PREFETCH].k *
						sizeof(struct btree_u64_block)
				),
				0,
				3
			);
		}
		struct context *c = &ctx[i];
		if (c->k < nblocks) {
			const struct btree_u64_block *block =
				(const struct btree_u64_block *)big_array_get(
					&btree->array,
					c->k * sizeof(struct btree_u64_block)
				);

			// Search within block using SIMD (GT comparison)
			size_t idx =
				btree_u64_block_search_gt(block, c->target);

			// Update result index
			c->result *= (BTREE_U64_BLOCK_SIZE + 1);
			c->result += idx;
		} else {
			c->result += btree->max_h_cnt;
		}
		result[i] = (c->result < btree->n) ? c->result : btree->n;
	}

	return count;
}

static inline size_t
btree_u64_lower_bounds(
	struct btree_u64 *btree,
	uint64_t *values,
	size_t count,
	uint32_t *result
) {
	struct context {
		size_t result;
		size_t k;
		__m256i target;
	} ctx[btree_u64_max_batch_size];

	if (count > btree_u64_max_batch_size) {
		count = btree_u64_max_batch_size;
	}

	// initialize context
	for (size_t i = 0; i < count; ++i) {
		struct context *c = &ctx[i];
		c->result = 0;
		c->k = 0;
		c->target =
			_mm256_set1_epi64x(values[i] ^ 0x8000000000000000ULL);
		;
	}

	const size_t nblocks = btree_u64_nblocks(btree);

	for (size_t step = 0; step < btree->h; ++step) {
		for (size_t i = 0; i < count; ++i) {
			if (PREFETCH > 0 && i + PREFETCH < count) {
				__builtin_prefetch(
					big_array_get(
						&btree->array,
						ctx[i + PREFETCH].k *
							sizeof(struct
							       btree_u64_block)
					),
					0,
					3
				);
			}

			struct context *c = &ctx[i];
			const struct btree_u64_block *block =
				(const struct btree_u64_block *)big_array_get(
					&btree->array,
					c->k * sizeof(struct btree_u64_block)
				);

			// Search within block using SIMD
			size_t idx = btree_u64_block_search(block, c->target);

			// Update result index
			c->result *= (BTREE_U64_BLOCK_SIZE + 1);
			c->result += idx;

			// Move to the next block
			c->k = btree_u64_next(c->k, idx);
		}
	}

	for (size_t i = 0; i < count; ++i) {
		if (PREFETCH > 0 && i + PREFETCH < count) {
			__builtin_prefetch(
				big_array_get(
					&btree->array,
					ctx[i + PREFETCH].k *
						sizeof(struct btree_u64_block)
				),
				0,
				3
			);
		}
		struct context *c = &ctx[i];
		if (c->k < nblocks) {
			const struct btree_u64_block *block =
				(const struct btree_u64_block *)big_array_get(
					&btree->array,
					c->k * sizeof(struct btree_u64_block)
				);

			// Search within block using SIMD
			size_t idx = btree_u64_block_search(block, c->target);

			// Update result index
			c->result *= (BTREE_U64_BLOCK_SIZE + 1);
			c->result += idx;
		} else {
			c->result += btree->max_h_cnt;
		}
		result[i] = (c->result < btree->n) ? c->result : btree->n;
	}

	return count;
}

#undef PREFETCH