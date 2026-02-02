# BTree Implementation Plan

## Overview

This document provides detailed specifications for implementing the type-specific btree variants. This serves as a blueprint for the Code mode implementation.

## File Structure

```
common/btree/
├── u32.h                      # uint32_t btree implementation
├── u64.h                      # uint64_t btree implementation
├── ARCHITECTURE.md            # Architecture documentation
├── MIGRATION_GUIDE.md         # Migration guide
└── IMPLEMENTATION_PLAN.md     # This file
```

## Implementation Specifications

### 1. common/btree/u32.h

#### Header Guards and Includes

```c
#pragma once

#include "../big_array.h"
#include <assert.h>
#include <immintrin.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
```

#### Constants

```c
// Block size for uint32_t: 64 bytes / 4 bytes = 16 elements
#define BTREE_U32_BLOCK_SIZE 16
```

#### Structures

```c
/**
 * @brief 64-byte aligned block storing 16 uint32_t values
 */
struct btree_u32_block {
    uint32_t values[BTREE_U32_BLOCK_SIZE];
} __attribute__((aligned(64)));

/**
 * @brief B-tree structure for uint32_t values
 * 
 * Stores sorted uint32_t values in a cache-optimized tree structure
 * for fast binary search operations using SIMD instructions.
 */
struct btree_u32 {
    struct big_array array;  // Storage for tree blocks
    size_t n;                // Number of elements
    size_t h;                // Tree height
    size_t max_h_cnt;        // Count of elements at maximum height
};
```

#### Private Helper Functions

```c
/**
 * @brief Get number of blocks in the btree
 */
static inline size_t
btree_u32_nblocks(const struct btree_u32 *btree);

/**
 * @brief Calculate next node index in tree traversal
 * @param v Current node index
 * @param i Position within current block
 */
static inline size_t
btree_u32_next(size_t v, size_t i);

/**
 * @brief Search within a single block using AVX2 SIMD
 * @param block Pointer to block to search
 * @param value Value to search for
 * @return Index of first element >= value, or BTREE_U32_BLOCK_SIZE if all < value
 */
static inline size_t
btree_u32_block_search(const struct btree_u32_block *block, uint32_t value);

/**
 * @brief Get GTE mask using AVX2 for 8 uint32_t elements
 * @param target Target value (broadcasted to all lanes)
 * @param data Pointer to 8 uint32_t values
 * @return Bitmask where bit i is set if data[i] >= target
 */
static inline int
btree_u32_get_gte_mask_avx2(__m256i target, const uint32_t *data);

/**
 * @brief Recursive tree building function
 * @param btree Tree being built
 * @param v Current node index
 * @param data Source data array
 * @param idx Current position in source data (modified)
 * @param n Total number of elements
 * @param h Current height in tree
 */
static void
btree_u32_build(
    struct btree_u32 *btree,
    size_t v,
    const uint32_t *data,
    size_t *idx,
    size_t n,
    size_t h
);
```

#### Public API Functions

```c
/**
 * @brief Initialize a btree with uint32_t values
 * 
 * Creates a cache-optimized search tree from sorted uint32_t data.
 * The tree uses 64-byte aligned blocks for cache efficiency and
 * AVX2 SIMD instructions for fast searching.
 * 
 * @param btree Pointer to uninitialized btree structure
 * @param data Pointer to sorted uint32_t array
 * @param n Number of elements in data array
 * @param mctx Memory context for allocations
 * @return 0 on success, -1 on allocation failure
 * 
 * @note Data array must be sorted in ascending order
 * @note On failure, btree is left in a safe state (can be freed)
 * 
 * Example:
 * @code
 * struct btree_u32 tree;
 * uint32_t data[] = {1, 5, 10, 15, 20};
 * int ret = btree_u32_init(&tree, data, 5, &mctx);
 * if (ret != 0) {
 *     // Handle error
 * }
 * @endcode
 */
int
btree_u32_init(
    struct btree_u32 *btree,
    const uint32_t *data,
    size_t n,
    struct memory_context *mctx
);

/**
 * @brief Free all memory associated with a btree
 * 
 * Releases all allocated memory and zeros the structure.
 * Safe to call multiple times on the same btree.
 * 
 * @param btree Pointer to btree to free
 * 
 * Example:
 * @code
 * btree_u32_free(&tree);
 * @endcode
 */
void
btree_u32_free(struct btree_u32 *btree);

/**
 * @brief Find first element >= value (lower bound)
 * 
 * Returns the index of the first element that is not less than
 * the given value. If all elements are less than value, returns n.
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
 * size_t idx = btree_u32_lower_bound(&tree, 12);
 * // Returns 2 (index of 15, first element >= 12)
 * @endcode
 */
size_t
btree_u32_lower_bound(const struct btree_u32 *btree, uint32_t value);

/**
 * @brief Find first element > value (upper bound)
 * 
 * Returns the index of the first element that is greater than
 * the given value. If all elements are <= value, returns n.
 * 
 * @param btree Pointer to initialized btree
 * @param value Value to search for
 * @return Index of first element > value, or n if not found
 * 
 * Time complexity: O(log n) with SIMD acceleration
 * 
 * Example:
 * @code
 * // Tree contains: [1, 5, 10, 15, 20]
 * size_t idx = btree_u32_upper_bound(&tree, 10);
 * // Returns 3 (index of 15, first element > 10)
 * @endcode
 */
static inline size_t
btree_u32_upper_bound(const struct btree_u32 *btree, uint32_t value);
```

#### Implementation Notes for u32.h

1. **Block Structure**: Array of 16 uint32_t values (64 bytes total)
2. **SIMD Search**: Process 16 elements per block in two AVX2 operations (8 elements each)
3. **Tree Structure**: Implicit tree structure using index arithmetic
4. **Upper Bound**: Implemented as `lower_bound(value + 1)` for efficiency

### 2. common/btree/u64.h

#### Header Guards and Includes

```c
#pragma once

#include "../big_array.h"
#include <assert.h>
#include <immintrin.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
```

#### Constants

```c
// Block size for uint64_t: 64 bytes / 8 bytes = 8 elements
#define BTREE_U64_BLOCK_SIZE 8
```

#### Structures

```c
/**
 * @brief 64-byte aligned block storing 8 uint64_t values
 */
struct btree_u64_block {
    uint64_t values[BTREE_U64_BLOCK_SIZE];
} __attribute__((aligned(64)));

/**
 * @brief B-tree structure for uint64_t values
 * 
 * Stores sorted uint64_t values in a cache-optimized tree structure
 * for fast binary search operations using SIMD instructions.
 */
struct btree_u64 {
    struct big_array array;  // Storage for tree blocks
    size_t n;                // Number of elements
    size_t h;                // Tree height
    size_t max_h_cnt;        // Count of elements at maximum height
};
```

#### Private Helper Functions

```c
/**
 * @brief Get number of blocks in the btree
 */
static inline size_t
btree_u64_nblocks(const struct btree_u64 *btree);

/**
 * @brief Calculate next node index in tree traversal
 * @param v Current node index
 * @param i Position within current block
 */
static inline size_t
btree_u64_next(size_t v, size_t i);

/**
 * @brief Search within a single block using AVX2 SIMD
 * @param block Pointer to block to search
 * @param value Value to search for
 * @return Index of first element >= value, or BTREE_U64_BLOCK_SIZE if all < value
 */
static inline size_t
btree_u64_block_search(const struct btree_u64_block *block, uint64_t value);

/**
 * @brief Get GTE mask using AVX2 for 4 uint64_t elements
 * @param target_signed Target value XORed with sign bit
 * @param sign_bit Sign bit mask for unsigned comparison trick
 * @param data Pointer to 4 uint64_t values
 * @return Bitmask where bit i is set if data[i] >= target
 */
static inline int
btree_u64_get_gte_mask_avx2(
    __m256i target_signed,
    __m256i sign_bit,
    const uint64_t *data
);

/**
 * @brief Recursive tree building function
 * @param btree Tree being built
 * @param v Current node index
 * @param data Source data array
 * @param idx Current position in source data (modified)
 * @param n Total number of elements
 * @param h Current height in tree
 */
static void
btree_u64_build(
    struct btree_u64 *btree,
    size_t v,
    const uint64_t *data,
    size_t *idx,
    size_t n,
    size_t h
);
```

#### Public API Functions

```c
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
 * Example:
 * @code
 * struct btree_u64 tree;
 * uint64_t data[] = {100, 500, 1000, 1500, 2000};
 * int ret = btree_u64_init(&tree, data, 5, &mctx);
 * if (ret != 0) {
 *     // Handle error
 * }
 * @endcode
 */
int
btree_u64_init(
    struct btree_u64 *btree,
    const uint64_t *data,
    size_t n,
    struct memory_context *mctx
);

/**
 * @brief Free all memory associated with a btree
 * 
 * Releases all allocated memory and zeros the structure.
 * Safe to call multiple times on the same btree.
 * 
 * @param btree Pointer to btree to free
 * 
 * Example:
 * @code
 * btree_u64_free(&tree);
 * @endcode
 */
void
btree_u64_free(struct btree_u64 *btree);

/**
 * @brief Find first element >= value (lower bound)
 * 
 * Returns the index of the first element that is not less than
 * the given value. If all elements are less than value, returns n.
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
 * @endcode
 */
size_t
btree_u64_lower_bound(const struct btree_u64 *btree, uint64_t value);

/**
 * @brief Find first element > value (upper bound)
 * 
 * Returns the index of the first element that is greater than
 * the given value. If all elements are <= value, returns n.
 * 
 * @param btree Pointer to initialized btree
 * @param value Value to search for
 * @return Index of first element > value, or n if not found
 * 
 * Time complexity: O(log n) with SIMD acceleration
 * 
 * Example:
 * @code
 * // Tree contains: [100, 500, 1000, 1500, 2000]
 * size_t idx = btree_u64_upper_bound(&tree, 1000);
 * // Returns 3 (index of 1500, first element > 1000)
 * @endcode
 */
static inline size_t
btree_u64_upper_bound(const struct btree_u64 *btree, uint64_t value);
```

#### Implementation Notes for u64.h

1. **Block Structure**: Array of 8 uint64_t values (64 bytes total)
2. **SIMD Search**: Process 8 elements per block in two AVX2 operations (4 elements each)
3. **Unsigned Comparison Trick**: XOR with sign bit to use signed comparison for unsigned values
4. **Upper Bound**: Implemented as `lower_bound(value + 1)` for efficiency

## Key Implementation Details

### Block Structure Rationale

**uint32_t blocks:**
```c
struct btree_u32_block {
    uint32_t values[16];  // 16 * 4 = 64 bytes
} __attribute__((aligned(64)));
```

**uint64_t blocks:**
```c
struct btree_u64_block {
    uint64_t values[8];   // 8 * 8 = 64 bytes
} __attribute__((aligned(64)));
```

This direct array storage:
- Simplifies pointer arithmetic
- Makes SIMD operations more straightforward
- Maintains cache alignment
- Provides type safety

### SIMD Optimization Strategy

#### uint32_t (16 elements per block)
```c
static inline size_t
btree_u32_block_search(const struct btree_u32_block *block, uint32_t value) {
    __m256i target = _mm256_set1_epi32(value);
    
    // Process first 8 elements
    int mask1 = btree_u32_get_gte_mask_avx2(target, block->values);
    // Process next 8 elements
    int mask2 = btree_u32_get_gte_mask_avx2(target, block->values + 8);
    
    // Combine masks
    unsigned long long combined = mask1 | (mask2 << 8);
    // Add sentinel bit
    combined |= (1ULL << BTREE_U32_BLOCK_SIZE);
    
    // Find first set bit
    return __builtin_ffsll(combined) - 1;
}
```

#### uint64_t (8 elements per block)
```c
static inline size_t
btree_u64_block_search(const struct btree_u64_block *block, uint64_t value) {
    // Prepare for unsigned comparison using signed instructions
    __m256i sign_bit = _mm256_set1_epi64x(0x8000000000000000ULL);
    __m256i target = _mm256_set1_epi64x(value);
    __m256i target_signed = _mm256_xor_si256(target, sign_bit);
    
    // Process first 4 elements
    int mask1 = btree_u64_get_gte_mask_avx2(target_signed, sign_bit, block->values);
    // Process next 4 elements
    int mask2 = btree_u64_get_gte_mask_avx2(target_signed, sign_bit, block->values + 4);
    
    // Combine masks
    unsigned long long combined = mask1 | (mask2 << 4);
    // Add sentinel bit
    combined |= (1ULL << BTREE_U64_BLOCK_SIZE);
    
    // Find first set bit
    return __builtin_ffsll(combined) - 1;
}
```

### Tree Structure

The tree uses implicit indexing:
- Root at index 0
- For node at index `v` with block size `b`:
  - Child `i` is at index `v * (b + 1) + i + 1`
  - This creates a (b+1)-ary tree

### Memory Layout

```
Block 0 (root):     [e0, e1, e2, ..., e(b-1)]
                     |   |   |        |      |
Block 1:          [...]  |   |        |    [...]
Block 2:              [...] |        |
...                         |      [...]
Block b:                  [...]
Block b+1:                        [...]
```

### Initialization Algorithm

1. Calculate number of blocks needed: `(n + b - 1) / b`
2. Allocate big_array with `nblocks * sizeof(struct btree_uXX_block)` bytes
3. Initialize all block elements with maximum value (last element of data)
4. Recursively build tree from sorted data
5. Track tree height and element count at max height

### Search Algorithm

1. Start at root block (index 0)
2. Use SIMD to find first element >= target in current block
3. Calculate child index based on position found
4. Repeat until reaching a leaf (block index >= total blocks)
5. Adjust result based on tree height and max height count

## Code Quality Requirements

### Style Guidelines

1. **No Macros**: Except for include guards and constants
2. **Inline Functions**: Use `static inline` for small, performance-critical functions
3. **Documentation**: Every public function must have complete documentation
4. **Type Safety**: Explicit types, no void* except in big_array interface
5. **Error Handling**: Check all allocations, return appropriate error codes

### Performance Requirements

1. **Cache Alignment**: All blocks must be 64-byte aligned
2. **SIMD Usage**: Must use AVX2 for block searches
3. **Minimal Branching**: Avoid branches in hot paths
4. **Inline Critical Paths**: Mark hot functions as inline

### Testing Requirements

1. **Unit Tests**: Cover all edge cases
2. **Performance Tests**: Verify no regression vs original
3. **Memory Tests**: Check for leaks with valgrind
4. **Correctness Tests**: Verify search results match std::lower_bound/upper_bound

## Implementation Checklist

### For common/btree/u32.h
- [ ] Include guards and headers
- [ ] Define BTREE_U32_BLOCK_SIZE constant
- [ ] Define struct btree_u32_block with uint32_t values[16]
- [ ] Define struct btree_u32
- [ ] Implement btree_u32_nblocks()
- [ ] Implement btree_u32_next()
- [ ] Implement btree_u32_get_gte_mask_avx2()
- [ ] Implement btree_u32_block_search()
- [ ] Implement btree_u32_build()
- [ ] Implement btree_u32_init()
- [ ] Implement btree_u32_free()
- [ ] Implement btree_u32_lower_bound()
- [ ] Implement btree_u32_upper_bound()
- [ ] Add comprehensive documentation

### For common/btree/u64.h
- [ ] Include guards and headers
- [ ] Define BTREE_U64_BLOCK_SIZE constant
- [ ] Define struct btree_u64_block with uint64_t values[8]
- [ ] Define struct btree_u64
- [ ] Implement btree_u64_nblocks()
- [ ] Implement btree_u64_next()
- [ ] Implement btree_u64_get_gte_mask_avx2()
- [ ] Implement btree_u64_block_search()
- [ ] Implement btree_u64_build()
- [ ] Implement btree_u64_init()
- [ ] Implement btree_u64_free()
- [ ] Implement btree_u64_lower_bound()
- [ ] Implement btree_u64_upper_bound()
- [ ] Add comprehensive documentation

## Next Steps

After completing the implementation:

1. Update [`common/btree.h`](../btree.h) with deprecation notice
2. Run existing tests to verify compatibility
3. Create new tests using the function-based API
4. Benchmark performance against original implementation
5. Update documentation with examples

## References

- Original implementation: [`common/btree.h`](../btree.h)
- Architecture document: [ARCHITECTURE.md](ARCHITECTURE.md)
- Migration guide: [MIGRATION_GUIDE.md](MIGRATION_GUIDE.md)
- Test examples: [`tests/common/btree_test.c`](../../tests/common/btree_test.c)