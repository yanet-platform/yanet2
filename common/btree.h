/**
 * @file btree.h
 * @brief Cache-optimized B-tree implementation for fast lower_bound searches
 *
 * This B-tree implementation is optimized for cache performance by packing
 * elements into 64-byte cache-line-aligned nodes. It's designed for static
 * sorted arrays where fast binary search (lower_bound/upper_bound) operations
 * are needed.
 *
 * ## Key Features
 * - Cache-line optimized: 64-byte aligned nodes for minimal cache misses
 * - Implicit tree structure: no pointers, index-based navigation
 * - Efficient search: O(log n) lower_bound and upper_bound operations
 * - Memory efficient: uses big_array for large datasets
 * - Type-generic: works with any comparable type via macros
 *
 * ## Architecture
 * The B-tree stores elements in a breadth-first layout within cache-aligned
 * nodes. Each node contains b = 64/elem_size elements. The tree structure is
 * implicit - child indices are calculated using the formula:
 *   child(v, i) = v * (b + 1) + i + 1
 *
 * This layout ensures that during search operations, entire cache lines are
 * loaded at once, minimizing memory access latency.
 *
 * ## Memory Overhead
 * The btree allocates n * 64 bytes total, where n is the number of elements.
 * This means the memory overhead factor is B = 64/elem_size:
 * - uint8_t (1 byte): 64x overhead (64 bytes per element)
 * - uint16_t (2 bytes): 32x overhead (64 bytes per element)
 * - uint32_t (4 bytes): 16x overhead (64 bytes per element)
 * - uint64_t (8 bytes): 8x overhead (64 bytes per element)
 *
 * This overhead is the trade-off for cache-optimized performance. For small
 * element types, consider if the performance benefit justifies the memory cost.
 *
 * ## Usage Example
 * ```c
 * // Create sorted array
 * uint32_t data[] = {1, 5, 10, 15, 20, 25, 30};
 * size_t n = sizeof(data) / sizeof(data[0]);
 *
 * // Initialize btree
 * struct btree tree;
 * if (BTREE_INIT(&tree, data, n, &mctx) != 0) {
 *     // Handle error
 * }
 *
 * // Find lower bound (first element >= 12)
 * size_t idx = BTREE_LOWER_BOUND(&tree, 12);
 * // idx will be 3 (element 15)
 *
 * // Find upper bound (first element > 15)
 * idx = BTREE_UPPER_BOUND(&tree, 15);
 * // idx will be 4 (element 20)
 *
 * // Cleanup
 * BTREE_FREE(&tree);
 * ```
 *
 * ## Performance Characteristics
 * - Initialization: O(n) - builds tree from sorted array
 * - Search (lower_bound/upper_bound): O(log n) with excellent cache locality
 * - Memory: O(n * 64) bytes - significant overhead for cache optimization
 *
 * ## Thread Safety
 * The btree structure is not thread-safe. External synchronization is required
 * for concurrent access. However, multiple threads can safely access different
 * btree instances.
 *
 * @see big_array.h for underlying storage mechanism
 */
////////////////////////////////////////////////////////////////////////////////

#pragma once

#include "big_array.h"
#include <assert.h>
#include <string.h>

/**
 * @struct btree_node
 * @brief Cache-line aligned node containing btree elements
 *
 * Each node is exactly 64 bytes (one cache line) to optimize memory access
 * patterns. The number of elements per node depends on element size:
 * b = 64 / sizeof(element).
 */
struct btree_node {
	uint8_t bytes[64];
} __attribute__((aligned(64)));

/**
 * @struct btree
 * @brief B-tree container using implicit index-based structure
 *
 * The btree stores elements in a breadth-first layout within a big_array.
 * Tree navigation is performed using index calculations rather than pointers.
 */
struct btree {
	struct big_array array; ///< Array of btree_node structures
};

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
	size_t elem_size
) {
	if (v >= n) {
		return;
	}
	const size_t b = __btree_b(elem_size);
	for (size_t i = 0; i < b; ++i) {
		size_t next = __btree_next(v, b, i);
		__btree_build(btree, next, a, t, n, elem_size);
		const void *cur = a + (*t < n ? *t : n - 1) * elem_size;
		memcpy(big_array_get(&btree->array, *t++), cur, elem_size);
	}
	size_t next = __btree_next(v, b, b);
	__btree_build(btree, next, a, t, n, elem_size);
}

////////////////////////////////////////////////////////////////////////////////
/**
 * @brief Initialize a btree from a sorted array
 *
 * Constructs a cache-optimized B-tree from a sorted array. The input array
 * must be sorted in ascending order. The tree is built in O(n) time using
 * a recursive algorithm that arranges elements in a breadth-first layout
 * optimized for cache performance.
 *
 * The branching factor b is automatically calculated as 64/sizeof(element),
 * ensuring each node fits exactly in one cache line. For example:
 * - uint8_t: b = 64 elements per node
 * - uint16_t: b = 32 elements per node
 * - uint32_t: b = 16 elements per node
 * - uint64_t: b = 8 elements per node
 *
 * @param btree Pointer to uninitialized btree structure
 * @param a Sorted array of elements (must be in ascending order)
 * @param n Number of elements in the array
 * @param mctx Memory context for allocations (currently unused but kept for API
 * consistency)
 *
 * @return 0 on success, -1 on allocation failure
 *
 * @warning The input array 'a' MUST be sorted in ascending order. Behavior
 *          is undefined if the array is not sorted.
 * @note The btree makes a copy of the data, so the original array can be
 *       freed after initialization.
 * @note If n is 0, the function succeeds but creates an empty tree.
 * @note Memory usage: n * 64 bytes (significant overhead for small types)
 *
 * Example:
 * ```c
 * uint32_t sorted_data[] = {1, 3, 5, 7, 9, 11, 13, 15};
 * struct btree tree;
 * if (BTREE_INIT(&tree, sorted_data, 8, &mctx) != 0) {
 *     // Handle allocation failure
 * }
 * ```
 */
#define BTREE_INIT(btree_ptr, a, n, mctx)                                      \
	__extension__({                                                        \
		__label__ __done;                                              \
		size_t bytes = sizeof(struct btree_node) * n;                  \
		int __ret = 0;                                                 \
		if (big_array_init(&(btree_ptr)->array, bytes, mctx) != 0) {   \
			__ret = -1;                                            \
			goto __done;                                           \
		}                                                              \
		size_t t = 0;                                                  \
		__btree_build(btree_ptr, 0, a, &t, n, sizeof(a[0]));           \
	__done:                                                                \
		__ret;                                                         \
	})

/**
 * @brief Free all memory associated with a btree
 *
 * Releases all memory allocated for the btree, including the internal
 * big_array storage. After calling this function, the btree structure
 * is zeroed and cannot be used until re-initialized with BTREE_INIT.
 *
 * This function is safe to call multiple times on the same btree
 * (double-free safe).
 *
 * @param btree Pointer to btree to free
 *
 * @note After calling BTREE_FREE, the btree structure is in a safe state
 *       and can be re-initialized or discarded.
 * @note This function never fails and always leaves the btree in a
 *       consistent state.
 *
 * Example:
 * ```c
 * BTREE_FREE(&tree);
 * // tree is now safe to re-initialize or discard
 * ```
 */
#define BTREE_FREE(btree_ptr) big_array_free(&(btree_ptr)->array)

#define __BTREE_BLOCK_SEACH(btree_block, x)                                    \
	__extension__({                                                        \
		typeof((x)) __x = (x);                                         \
		const typeof(&__x) bytes =                                     \
			(const typeof(&__x))((btree_block)->bytes);            \
		size_t b = __btree_b(sizeof(__x));                             \
		int mask = (1 << b);                                           \
		for (size_t i = 0; i < b; ++i) {                               \
			mask |= (bytes[i] >= __x) << i;                        \
		}                                                              \
		__builtin_ffs(mask) - 1;                                       \
	})

/**
 * @brief Find the first element not less than the given value
 *
 * Performs a binary search to find the index of the first element in the
 * btree that is greater than or equal to x. This is equivalent to
 * std::lower_bound in C++.
 *
 * The search is cache-optimized, traversing the implicit tree structure
 * by loading entire cache lines at once. Time complexity is O(log n) with
 * excellent cache locality.
 *
 * @param btree Pointer to initialized btree
 * @param x Value to search for (must be same type as btree elements)
 *
 * @return Index of the first element >= x, or n if all elements are < x
 *
 * @note The returned index is in the range [0, n] where n is the number
 *       of elements. If the index equals n, no element >= x was found.
 * @note The comparison uses the >= operator, so the element type must
 *       support this operation.
 *
 * Example:
 * ```c
 * // Tree contains: [1, 5, 10, 15, 20, 25, 30]
 * size_t idx;
 *
 * idx = BTREE_LOWER_BOUND(&tree, 12);  // Returns 3 (element 15)
 * idx = BTREE_LOWER_BOUND(&tree, 15);  // Returns 3 (element 15)
 * idx = BTREE_LOWER_BOUND(&tree, 0);   // Returns 0 (element 1)
 * idx = BTREE_LOWER_BOUND(&tree, 100); // Returns 7 (past end)
 * ```
 */
#define BTREE_LOWER_BOUND(btree_ptr, x)                                        \
	__extension__({                                                        \
		typeof(x) __x = (x);                                           \
		const size_t n =                                               \
			(btree_ptr)->array.size / sizeof(struct btree_node);   \
		const size_t b = __btree_b(sizeof(__x));                       \
		size_t res = n;                                                \
		size_t k = 0;                                                  \
		while (k < n) {                                                \
			size_t i = __BTREE_BLOCK_SEACH(                        \
				(struct btree_node *)big_array_get(            \
					&(btree_ptr)->array,                   \
					k * sizeof(struct btree_node)          \
				),                                             \
				__x                                            \
			);                                                     \
			size_t next = __btree_next(k, b, i);                   \
			if (next < n) {                                        \
				res = next;                                    \
			}                                                      \
			k = next;                                              \
		}                                                              \
		res;                                                           \
	})

/**
 * @brief Find the first element greater than the given value
 *
 * Performs a binary search to find the index of the first element in the
 * btree that is strictly greater than x. This is equivalent to
 * std::upper_bound in C++.
 *
 * This is implemented as BTREE_LOWER_BOUND(btree, x + 1), which works
 * correctly for integer types. The search has the same O(log n) time
 * complexity with excellent cache locality.
 *
 * @param btree Pointer to initialized btree
 * @param x Value to search for (must be same type as btree elements)
 *
 * @return Index of the first element > x, or n if all elements are <= x
 *
 * @note The returned index is in the range [0, n] where n is the number
 *       of elements. If the index equals n, no element > x was found.
 * @note This macro adds 1 to x, so it's designed for integer types.
 *       For floating-point types, consider using BTREE_LOWER_BOUND with
 *       an appropriately incremented value.
 *
 * Example:
 * ```c
 * // Tree contains: [1, 5, 10, 15, 20, 25, 30]
 * size_t idx;
 *
 * idx = BTREE_UPPER_BOUND(&tree, 12);  // Returns 3 (element 15)
 * idx = BTREE_UPPER_BOUND(&tree, 15);  // Returns 4 (element 20)
 * idx = BTREE_UPPER_BOUND(&tree, 0);   // Returns 0 (element 1)
 * idx = BTREE_UPPER_BOUND(&tree, 30);  // Returns 7 (past end)
 * ```
 */
#define BTREE_UPPER_BOUND(btree_ptr, x) BTREE_LOWER_BOUND(btree_ptr, (x) + 1)
