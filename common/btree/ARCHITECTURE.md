# BTree Refactoring Architecture

## Overview

This document describes the architecture for separating the generic btree implementation into two type-specific implementations for `uint32_t` and `uint64_t`.

## Current Implementation Analysis

### Structure
The current [`btree.h`](../btree.h) uses C macros and `_Generic` to provide a type-generic B-tree implementation:

- **Core Structure**: `struct btree` with `big_array` for storage
- **API**: Macro-based (`BTREE_INIT`, `BTREE_LOWER_BOUND`, `BTREE_UPPER_BOUND`, `BTREE_FREE`)
- **Type Detection**: Uses `typeof_eq` macro with `_Generic` for runtime type selection
- **SIMD Optimization**: AVX2 intrinsics for fast searching
  - `__get_gte_mask_avx2()` for uint32_t (8 elements per 256-bit vector)
  - `__get_gte_mask_avx2_64()` for uint64_t (4 elements per 256-bit vector)

### Key Components

1. **Block Structure**: 64-byte aligned blocks (`struct btree_block`)
2. **Tree Metadata**: Size, height, max height count
3. **Search Algorithm**: Block-based binary search with SIMD acceleration
4. **Build Algorithm**: Recursive tree construction

## New Architecture

### Design Principles

1. **No Macros**: Replace all macros with inline functions for type safety
2. **Type-Specific**: Separate implementations optimized for each type
3. **Backward Compatible**: Keep original `btree.h` with deprecation notice
4. **Performance**: Maintain or improve performance with specialized code

### File Structure

```
common/
├── btree.h                    # Original (deprecated, includes new headers)
└── btree/
    ├── u32.h                  # uint32_t implementation
    ├── u64.h                  # uint64_t implementation
    └── ARCHITECTURE.md        # This file
```

### Type-Specific Structures

#### common/btree/u32.h
```c
struct btree_u32 {
    struct big_array array;
    size_t n;           // Number of elements
    size_t h;           // Tree height
    size_t max_h_cnt;   // Count at max height
};
```

#### common/btree/u64.h
```c
struct btree_u64 {
    struct big_array array;
    size_t n;           // Number of elements
    size_t h;           // Tree height
    size_t max_h_cnt;   // Count at max height
};
```

### API Design

#### Function Naming Convention
- Prefix: `btree_u32_` or `btree_u64_`
- Clear, descriptive names
- No macros

#### Core API Functions

**Initialization & Cleanup:**
```c
// uint32_t version
int btree_u32_init(struct btree_u32 *btree, const uint32_t *data, 
                   size_t n, struct memory_context *mctx);
void btree_u32_free(struct btree_u32 *btree);

// uint64_t version
int btree_u64_init(struct btree_u64 *btree, const uint64_t *data,
                   size_t n, struct memory_context *mctx);
void btree_u64_free(struct btree_u64 *btree);
```

**Search Operations:**
```c
// uint32_t version
size_t btree_u32_lower_bound(const struct btree_u32 *btree, uint32_t value);
size_t btree_u32_upper_bound(const struct btree_u32 *btree, uint32_t value);

// uint64_t version
size_t btree_u64_lower_bound(const struct btree_u64 *btree, uint64_t value);
size_t btree_u64_upper_bound(const struct btree_u64 *btree, uint64_t value);
```

### Implementation Details

#### Constants
```c
// uint32_t: 64 bytes / 4 bytes = 16 elements per block
#define BTREE_U32_BLOCK_SIZE 16

// uint64_t: 64 bytes / 8 bytes = 8 elements per block
#define BTREE_U64_BLOCK_SIZE 8
```

#### SIMD Optimization

**uint32_t Search (16 elements per block):**
- Process 8 elements at a time using `_mm256_load_si256`
- Two AVX2 operations per block
- Use `_mm256_max_epu32` and `_mm256_cmpeq_epi32`

**uint64_t Search (8 elements per block):**
- Process 4 elements at a time using `_mm256_load_si256`
- Two AVX2 operations per block
- Use signed comparison trick with XOR for unsigned comparison

#### Helper Functions

Each implementation will have private helper functions:

```c
// Block size calculation
static inline size_t btree_u32_block_size(void);
static inline size_t btree_u64_block_size(void);

// Next node calculation
static inline size_t btree_u32_next(size_t v, size_t i);
static inline size_t btree_u64_next(size_t v, size_t i);

// Block search with SIMD
static inline size_t btree_u32_block_search(
    const struct btree_block *block, uint32_t value);
static inline size_t btree_u64_block_search(
    const struct btree_block *block, uint64_t value);

// Recursive build
static void btree_u32_build(struct btree_u32 *btree, size_t v,
    const uint32_t *data, size_t *idx, size_t n, size_t h);
static void btree_u64_build(struct btree_u64 *btree, size_t v,
    const uint64_t *data, size_t *idx, size_t n, size_t h);
```

### Backward Compatibility

The original [`btree.h`](../btree.h) will be updated to:

1. Include both new headers
2. Add deprecation warnings
3. Provide compatibility macros that map to new functions
4. Maintain existing macro-based API for gradual migration

Example:
```c
#pragma once

// Deprecation notice
#warning "btree.h is deprecated. Use btree/u32.h or btree/u64.h instead."

#include "btree/u32.h"
#include "btree/u64.h"

// Backward compatibility macros
#define BTREE_INIT(btree_ptr, a, size, mctx) \
    _Generic((a)[0], \
        uint32_t: btree_u32_init((struct btree_u32*)(btree_ptr), (a), (size), (mctx)), \
        uint64_t: btree_u64_init((struct btree_u64*)(btree_ptr), (a), (size), (mctx)) \
    )

// ... similar for other macros
```

## Migration Path

### Phase 1: Create New Implementations
1. Implement [`common/btree/u32.h`](u32.h)
2. Implement [`common/btree/u64.h`](u64.h)
3. Add comprehensive inline documentation

### Phase 2: Update Tests
1. Create new test files using function-based API
2. Verify performance matches or exceeds original
3. Test edge cases and boundary conditions

### Phase 3: Gradual Migration
1. Update [`common/btree.h`](../btree.h) with deprecation notice
2. Provide migration guide
3. Update existing code incrementally

### Phase 4: Cleanup (Future)
1. Remove deprecated [`btree.h`](../btree.h) after migration period
2. Update all references in codebase

## Performance Considerations

### Optimizations Preserved
- Cache-aligned 64-byte blocks
- AVX2 SIMD instructions for parallel comparison
- Efficient bit manipulation for indexing
- Minimal branching in hot paths

### Expected Performance
- **Initialization**: O(n) - same as original
- **Search**: O(log n) with SIMD acceleration - same or better
- **Memory**: Same footprint as original

### Benchmarking
Use existing benchmark files:
- [`tests/common/btree_bench_u32.c`](../../tests/common/btree_bench_u32.c)
- [`tests/common/btree_bench_u64.c`](../../tests/common/btree_bench_u64.c)

## Code Quality

### Standards
- No macros in new implementations (except include guards and constants)
- Inline functions for performance-critical code
- Static functions for internal helpers
- Comprehensive documentation
- Type safety through explicit types

### Documentation
Each function will include:
- Purpose and behavior
- Parameter descriptions
- Return value specification
- Performance characteristics
- Usage examples

## Testing Strategy

### Unit Tests
- Empty tree
- Single element
- Power-of-two sizes (16, 64, 256, etc.)
- Non-power-of-two sizes
- Boundary values (0, UINT32_MAX, UINT64_MAX)
- Duplicate values
- Sequential and random data

### Performance Tests
- Large datasets (millions of elements)
- Search patterns (sequential, random, worst-case)
- Memory usage validation
- Comparison with original implementation

## Dependencies

### Required Headers
- `<stdint.h>` - Fixed-width integer types
- `<stddef.h>` - size_t
- `<string.h>` - memcpy, memset
- `<immintrin.h>` - AVX2 intrinsics
- `<assert.h>` - Assertions for debug builds
- [`../big_array.h`](../big_array.h) - Large array support
- [`../memory.h`](../memory.h) - Memory context

### Build Requirements
- Compiler with AVX2 support
- C11 or later (for inline functions)

## Future Enhancements

### Potential Improvements
1. **Additional Types**: Support for uint16_t, uint8_t if needed
2. **Generic Template**: C++ template version for type safety
3. **Parallel Build**: Multi-threaded tree construction
4. **Compressed Storage**: For sparse datasets
5. **Iterator API**: For range queries

### Compatibility
- Maintain ABI stability within major versions
- Document breaking changes clearly
- Provide migration tools if needed

## Conclusion

This refactoring improves:
- **Type Safety**: Explicit types instead of generic macros
- **Maintainability**: Clearer code without macro complexity
- **Performance**: Specialized implementations can be better optimized
- **Documentation**: Function-based API is easier to document
- **Debugging**: Easier to step through and understand

The migration path ensures backward compatibility while enabling gradual adoption of the new API.