#pragma once

#include "memory.h"
#include "memory_address.h"
#include "memory_block.h"
#include <stddef.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////
/**
 * @file big_array.h
 * @brief Large array implementation that exceeds single memory block limits
 *
 * The big_array provides a transparent interface for managing arrays that are
 * larger than MEMORY_BLOCK_ALLOCATOR_MAX_SIZE by automatically splitting them
 * into multiple fixed-size subarrays.
 *
 * ## Key Features
 * - Transparent access to large arrays via index-based API
 * - Automatic subarray management and allocation
 * - Uses relative pointers for shared memory compatibility
 * - Memory-efficient: only allocates what's needed
 * - Safe cleanup with double-free protection
 *
 * ## Architecture
 * The big_array splits a large logical array into multiple subarrays. All
 * subarrays except the last have size equal to MEMORY_BLOCK_ALLOCATOR_MAX_SIZE
 * (typically 64MB). The last subarray is allocated with only the remaining size
 * to avoid wasting memory. The implementation uses bit manipulation to
 * efficiently calculate which subarray contains a given index and the offset
 * within that subarray.
 *
 * Memory layout:
 * ```
 * big_array
 *   ├─> subarrays[] (array of pointers to subarrays)
 *   │     ├─> subarray[0] (size: 1 << subarray_len_exp, e.g., 64MB)
 *   │     ├─> subarray[1] (size: 1 << subarray_len_exp, e.g., 64MB)
 *   │     └─> subarray[N] (size: remainder, e.g., 10MB if total is 138MB)
 *   └─> mctx (memory context for tracking allocations)
 * ```
 *
 * ## Usage Example
 * ```c
 * struct block_allocator ba;
 * block_allocator_init(&ba);
 * // ... setup arena ...
 *
 * struct memory_context mctx;
 * memory_context_init(&mctx, "my_context", &ba);
 *
 * // Create array larger than max block size
 * struct big_array array;
 * size_t size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE * 3;
 * if (big_array_init(&array, size, &mctx) != 0) {
 *     // Handle error
 * }
 *
 * // Access elements by index
 * uint8_t *ptr = (uint8_t *)big_array_get(&array, 12345);
 * *ptr = 0x42;
 *
 * // Cleanup
 * big_array_free(&array);
 * ```
 *
 * ## Performance Characteristics
 * - Initialization: O(N) where N is number of subarrays
 * - Access: O(1) - simple bit shift and pointer arithmetic
 * - Memory overhead: sizeof(void*) * number_of_subarrays
 * - Cleanup: O(N) where N is number of subarrays
 *
 * ## Thread Safety
 * The big_array structure itself is not thread-safe. External synchronization
 * is required if multiple threads access the same big_array concurrently.
 * However, different threads can safely access different big_array instances.
 *
 * @see memory.h for memory context management
 * @see memory_block.h for block allocator details
 */
////////////////////////////////////////////////////////////////////////////////

/**
 * @struct big_array
 * @brief Container for managing large arrays split across multiple subarrays
 *
 * This structure maintains metadata about a large array that has been split
 * into multiple fixed-size subarrays. All pointers use relative addressing
 * for shared memory compatibility.
 */
struct big_array {
	/**
	 * @brief Array of pointers to subarrays (using relative pointers)
	 *
	 * Each element points to a subarray. All subarrays except possibly the
	 * last have size (1 << subarray_len_exp). The last subarray has size
	 * equal to (size % (1 << subarray_len_exp)) or full size if evenly
	 * divisible. Uses relative pointer addressing via SET_OFFSET_OF/ADDR_OF
	 * macros.
	 */
	void **subarrays;

	/**
	 * @brief Number of subarrays allocated
	 *
	 * Calculated as: ceil(size / (1 << subarray_len_exp))
	 */
	size_t subarrays_count;

	/**
	 * @brief Log2 of subarray size (exponent)
	 *
	 * Each subarray (except possibly the last) has size (1 <<
	 * subarray_len_exp), which equals MEMORY_BLOCK_ALLOCATOR_MAX_SIZE. This
	 * allows efficient index calculation using bit shifts instead of
	 * division.
	 */
	size_t subarray_len_exp;

	/**
	 * @brief Total size of the array in bytes
	 *
	 * This is the original size parameter passed to big_array_init().
	 * Used to calculate the size of the last subarray during cleanup.
	 */
	size_t size;

	/**
	 * @brief Memory context for tracking allocations
	 *
	 * Child context derived from the parent context passed to
	 * big_array_init. Used to track all memory allocations made by this
	 * big_array.
	 */
	struct memory_context mctx;
};

static inline void
big_array_free(struct big_array *array);

/**
 * @brief Initialize a big_array with the specified size
 *
 * Allocates and initializes a big_array capable of holding 'size' bytes.
 * The array is split into multiple subarrays. All subarrays except the last
 * have size MEMORY_BLOCK_ALLOCATOR_MAX_SIZE. The last subarray is allocated
 * with only the remaining size (size % MEMORY_BLOCK_ALLOCATOR_MAX_SIZE) to
 * avoid wasting memory.
 *
 * @param array Pointer to uninitialized big_array structure
 * @param size Total size in bytes for the array
 * @param mctx Parent memory context for allocations
 *
 * @return 0 on success, -1 on allocation failure
 *
 * @note On failure, the function calls big_array_free() to clean up any
 *       partial allocations, so the array is left in a safe state.
 * @note If size is 0, the function succeeds but allocates no subarrays.
 *
 * Example:
 * ```c
 * struct big_array array;
 * if (big_array_init(&array, 1000000, &mctx) != 0) {
 *     // Handle allocation failure
 * }
 * ```
 */
static inline int
big_array_init(
	struct big_array *array, size_t size, struct memory_context *mctx
) {
	memory_context_init_from(&array->mctx, mctx, "big_array");
	array->size = size;
	array->subarray_len_exp =
		63 - __builtin_clzll(MEMORY_BLOCK_ALLOCATOR_MAX_SIZE);
	array->subarrays_count = (size + (1 << array->subarray_len_exp) - 1) >>
				 array->subarray_len_exp;

	// Handle zero size case - no subarrays needed
	if (array->subarrays_count == 0) {
		array->subarrays = NULL;
		return 0;
	}

	void **subarrays = memory_balloc(
		&array->mctx, sizeof(void *) * array->subarrays_count
	);
	if (subarrays == NULL) {
		goto free_on_error;
	}
	memset(subarrays, 0, sizeof(void *) * array->subarrays_count);
	SET_OFFSET_OF(&array->subarrays, subarrays);

	size_t subarray_size = 1 << array->subarray_len_exp;
	for (size_t i = 0; i < array->subarrays_count; ++i) {
		// Last subarray gets only the remaining size
		size_t alloc_size = subarray_size;
		if (i == array->subarrays_count - 1) {
			size_t remainder =
				size & ((1 << array->subarray_len_exp) - 1);
			if (remainder != 0) {
				alloc_size = remainder;
			}
		}

		void *subarray = memory_balloc(&array->mctx, alloc_size);
		if (subarray == NULL) {
			goto free_on_error;
		}
		SET_OFFSET_OF(subarrays + i, subarray);
	}
	return 0;

free_on_error:
	big_array_free(array);
	return -1;
}

/**
 * @brief Get pointer to element at specified index
 *
 * Returns a pointer to the byte at the given index within the big_array.
 * The function efficiently calculates which subarray contains the index
 * and returns a pointer to the appropriate location.
 *
 * @param array Pointer to initialized big_array
 * @param index Byte index into the array (0-based)
 *
 * @return Pointer to the byte at the specified index
 *
 * @warning The caller must ensure that index is within the bounds of the
 *          array size passed to big_array_init(). No bounds checking is
 *          performed for performance reasons.
 * @warning The returned pointer is only valid until big_array_free() is called.
 *
 * Implementation details:
 * - Uses bit shift (index >> subarray_len_exp) to find subarray index
 * - Pointer arithmetic handles offset within subarray automatically
 * - O(1) time complexity
 *
 * Example:
 * ```c
 * // Access element at index 12345
 * uint8_t *ptr = (uint8_t *)big_array_get(&array, 12345);
 * *ptr = 0xFF;
 *
 * // Access as different type
 * uint32_t *int_ptr = (uint32_t *)big_array_get(&array, 0);
 * *int_ptr = 0x12345678;
 * ```
 */
static inline void *
big_array_get(struct big_array *array, size_t index) {
	size_t subarray_index = index >> array->subarray_len_exp;
	size_t offset_in_subarray =
		index & ((1 << array->subarray_len_exp) - 1);
	void **subarrays = ADDR_OF(&array->subarrays);
	return ADDR_OF(subarrays + subarray_index) + offset_in_subarray;
}

/**
 * @brief Free all memory associated with a big_array
 *
 * Releases all subarrays and the subarray pointer array, then zeros out
 * the big_array structure. This function is safe to call multiple times
 * on the same array (double-free safe).
 *
 * Each subarray is freed with its actual allocated size. The last subarray
 * may have a smaller size than the others if the total array size was not
 * evenly divisible by MEMORY_BLOCK_ALLOCATOR_MAX_SIZE.
 *
 * @param array Pointer to big_array to free
 *
 * @note After calling this function, the array structure is zeroed and
 *       cannot be used until re-initialized with big_array_init().
 * @note This function is safe to call even if initialization failed or
 *       if the array was never initialized (subarrays == NULL).
 * @note All memory statistics are properly updated in the memory context.
 *
 * Example:
 * ```c
 * big_array_free(&array);
 * // array is now safe to re-initialize or discard
 * ```
 */
static inline void
big_array_free(struct big_array *array) {
	if (array->subarrays == NULL) {
		return;
	}
	void **subarrays = ADDR_OF(&array->subarrays);
	size_t subarray_size = 1 << array->subarray_len_exp;

	for (size_t i = 0; i < array->subarrays_count; ++i) {
		void *subarray = ADDR_OF(subarrays + i);
		if (subarray != NULL) {
			// Last subarray may have a different size
			size_t free_size = subarray_size;
			if (i == array->subarrays_count - 1) {
				size_t remainder =
					array->size &
					((1 << array->subarray_len_exp) - 1);
				if (remainder != 0) {
					free_size = remainder;
				}
			}

			memory_bfree(&array->mctx, subarray, free_size);
			subarrays[i] = NULL;
		}
	}
	memory_bfree(
		&array->mctx, subarrays, sizeof(void *) * array->subarrays_count
	);
	memset(array, 0, sizeof(struct big_array));
}