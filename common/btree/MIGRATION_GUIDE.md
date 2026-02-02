# BTree Migration Guide

## Overview

This guide helps you migrate from the macro-based generic btree API in [`common/btree.h`](../btree.h) to the new type-specific function-based APIs in [`common/btree/u32.h`](u32.h) and [`common/btree/u64.h`](u64.h).

## Why Migrate?

### Benefits of New API

1. **Type Safety**: Explicit types prevent accidental type mismatches
2. **Better IDE Support**: Function signatures are easier for IDEs to understand
3. **Clearer Code**: No macro magic, easier to read and debug
4. **Better Documentation**: Functions can be properly documented
5. **Improved Performance**: Type-specific optimizations

### Backward Compatibility

The original [`btree.h`](../btree.h) will remain available with deprecation warnings, allowing gradual migration.

## API Mapping

### Structure Names

| Old API | New API (uint32_t) | New API (uint64_t) |
|---------|-------------------|-------------------|
| `struct btree` | `struct btree_u32` | `struct btree_u64` |

### Function/Macro Mapping

| Old Macro | New Function (uint32_t) | New Function (uint64_t) |
|-----------|------------------------|------------------------|
| `BTREE_INIT(btree, data, n, mctx)` | `btree_u32_init(btree, data, n, mctx)` | `btree_u64_init(btree, data, n, mctx)` |
| `BTREE_FREE(btree)` | `btree_u32_free(btree)` | `btree_u64_free(btree)` |
| `BTREE_LOWER_BOUND(btree, value)` | `btree_u32_lower_bound(btree, value)` | `btree_u64_lower_bound(btree, value)` |
| `BTREE_UPPER_BOUND(btree, value)` | `btree_u32_upper_bound(btree, value)` | `btree_u64_upper_bound(btree, value)` |

## Migration Examples

### Example 1: Basic uint32_t Usage

**Before (Old API):**
```c
#include "common/btree.h"

struct btree tree;
uint32_t data[] = {1, 5, 10, 15, 20, 25, 30};
size_t n = sizeof(data) / sizeof(data[0]);

// Initialize
int ret = BTREE_INIT(&tree, data, n, &mctx);
if (ret != 0) {
    // Handle error
}

// Search
size_t idx = BTREE_LOWER_BOUND(&tree, 12);

// Cleanup
BTREE_FREE(&tree);
```

**After (New API):**
```c
#include "common/btree/u32.h"

struct btree_u32 tree;
uint32_t data[] = {1, 5, 10, 15, 20, 25, 30};
size_t n = sizeof(data) / sizeof(data[0]);

// Initialize
int ret = btree_u32_init(&tree, data, n, &mctx);
if (ret != 0) {
    // Handle error
}

// Search
size_t idx = btree_u32_lower_bound(&tree, 12);

// Cleanup
btree_u32_free(&tree);
```

### Example 2: Basic uint64_t Usage

**Before (Old API):**
```c
#include "common/btree.h"

struct btree tree;
uint64_t data[] = {100, 500, 1000, 1500, 2000};
size_t n = sizeof(data) / sizeof(data[0]);

// Initialize
int ret = BTREE_INIT(&tree, data, n, &mctx);
if (ret != 0) {
    // Handle error
}

// Search
size_t idx = BTREE_UPPER_BOUND(&tree, 1200);

// Cleanup
BTREE_FREE(&tree);
```

**After (New API):**
```c
#include "common/btree/u64.h"

struct btree_u64 tree;
uint64_t data[] = {100, 500, 1000, 1500, 2000};
size_t n = sizeof(data) / sizeof(data[0]);

// Initialize
int ret = btree_u64_init(&tree, data, n, &mctx);
if (ret != 0) {
    // Handle error
}

// Search
size_t idx = btree_u64_upper_bound(&tree, 1200);

// Cleanup
btree_u64_free(&tree);
```

### Example 3: In a Struct

**Before (Old API):**
```c
struct my_data {
    struct btree tree;
    // ... other fields
};

void init_my_data(struct my_data *data, uint32_t *values, size_t n) {
    BTREE_INIT(&data->tree, values, n, &mctx);
}

void search_my_data(struct my_data *data, uint32_t value) {
    size_t idx = BTREE_LOWER_BOUND(&data->tree, value);
    // ... use idx
}

void cleanup_my_data(struct my_data *data) {
    BTREE_FREE(&data->tree);
}
```

**After (New API):**
```c
struct my_data {
    struct btree_u32 tree;  // Explicit type
    // ... other fields
};

void init_my_data(struct my_data *data, uint32_t *values, size_t n) {
    btree_u32_init(&data->tree, values, n, &mctx);
}

void search_my_data(struct my_data *data, uint32_t value) {
    size_t idx = btree_u32_lower_bound(&data->tree, value);
    // ... use idx
}

void cleanup_my_data(struct my_data *data) {
    btree_u32_free(&data->tree);
}
```

### Example 4: Multiple Trees

**Before (Old API):**
```c
struct btree tree32;
struct btree tree64;

uint32_t data32[] = {1, 2, 3, 4, 5};
uint64_t data64[] = {100, 200, 300, 400, 500};

BTREE_INIT(&tree32, data32, 5, &mctx);
BTREE_INIT(&tree64, data64, 5, &mctx);

size_t idx32 = BTREE_LOWER_BOUND(&tree32, 3);
size_t idx64 = BTREE_LOWER_BOUND(&tree64, 250);

BTREE_FREE(&tree32);
BTREE_FREE(&tree64);
```

**After (New API):**
```c
struct btree_u32 tree32;
struct btree_u64 tree64;

uint32_t data32[] = {1, 2, 3, 4, 5};
uint64_t data64[] = {100, 200, 300, 400, 500};

btree_u32_init(&tree32, data32, 5, &mctx);
btree_u64_init(&tree64, data64, 5, &mctx);

size_t idx32 = btree_u32_lower_bound(&tree32, 3);
size_t idx64 = btree_u64_lower_bound(&tree64, 250);

btree_u32_free(&tree32);
btree_u64_free(&tree64);
```

## Step-by-Step Migration Process

### Step 1: Identify All Usages

Search your codebase for:
- `#include "common/btree.h"`
- `struct btree`
- `BTREE_INIT`
- `BTREE_FREE`
- `BTREE_LOWER_BOUND`
- `BTREE_UPPER_BOUND`

### Step 2: Determine Data Types

For each btree usage, identify whether it stores `uint32_t` or `uint64_t` values.

### Step 3: Update Includes

Replace:
```c
#include "common/btree.h"
```

With either:
```c
#include "common/btree/u32.h"  // for uint32_t
```
or:
```c
#include "common/btree/u64.h"  // for uint64_t
```

### Step 4: Update Structure Declarations

Replace:
```c
struct btree tree;
```

With:
```c
struct btree_u32 tree;  // for uint32_t
// or
struct btree_u64 tree;  // for uint64_t
```

### Step 5: Update Function Calls

Replace macro calls with function calls according to the mapping table above.

### Step 6: Test Thoroughly

- Compile and fix any type errors
- Run existing tests
- Verify performance hasn't regressed
- Check for memory leaks

## Common Pitfalls

### Pitfall 1: Type Mismatch

**Problem:**
```c
struct btree_u32 tree;
uint64_t data[] = {1, 2, 3};  // Wrong type!
btree_u32_init(&tree, data, 3, &mctx);  // Compiler error
```

**Solution:**
Use the correct btree type for your data:
```c
struct btree_u64 tree;
uint64_t data[] = {1, 2, 3};
btree_u64_init(&tree, data, 3, &mctx);
```

### Pitfall 2: Mixing Old and New APIs

**Problem:**
```c
#include "common/btree.h"
#include "common/btree/u32.h"

struct btree tree;  // Old style
BTREE_INIT(&tree, data, n, &mctx);  // Old API
size_t idx = btree_u32_lower_bound(&tree, value);  // New API - won't work!
```

**Solution:**
Use one API consistently:
```c
#include "common/btree/u32.h"

struct btree_u32 tree;
btree_u32_init(&tree, data, n, &mctx);
size_t idx = btree_u32_lower_bound(&tree, value);
```

### Pitfall 3: Forgetting to Update Cleanup

**Problem:**
```c
struct btree_u32 tree;
btree_u32_init(&tree, data, n, &mctx);
// ... use tree ...
BTREE_FREE(&tree);  // Old macro won't work with new struct
```

**Solution:**
```c
struct btree_u32 tree;
btree_u32_init(&tree, data, n, &mctx);
// ... use tree ...
btree_u32_free(&tree);  // Use new function
```

## Performance Considerations

### No Performance Loss

The new API maintains the same performance characteristics:
- Same memory layout
- Same SIMD optimizations
- Same algorithmic complexity

### Potential Improvements

Type-specific implementations may enable:
- Better compiler optimizations
- More aggressive inlining
- Reduced code size (no generic dispatch)

## Testing Your Migration

### Checklist

- [ ] All includes updated
- [ ] All structure declarations updated
- [ ] All BTREE_INIT calls replaced
- [ ] All BTREE_FREE calls replaced
- [ ] All BTREE_LOWER_BOUND calls replaced
- [ ] All BTREE_UPPER_BOUND calls replaced
- [ ] Code compiles without warnings
- [ ] All tests pass
- [ ] Performance benchmarks show no regression
- [ ] No memory leaks detected

### Validation Commands

```bash
# Search for old API usage
grep -r "BTREE_INIT" .
grep -r "BTREE_FREE" .
grep -r "BTREE_LOWER_BOUND" .
grep -r "BTREE_UPPER_BOUND" .
grep -r "struct btree[^_]" .

# Run tests
make test

# Run benchmarks
./btree_bench_u32
./btree_bench_u64
```

## Getting Help

### Resources

- [Architecture Documentation](ARCHITECTURE.md) - Detailed design information
- [API Reference](u32.h) - uint32_t API documentation
- [API Reference](u64.h) - uint64_t API documentation
- [Test Examples](../../tests/common/btree_test.c) - Usage examples

### Common Questions

**Q: Can I use both old and new APIs in the same project?**
A: Yes, during the migration period. The old API includes the new headers and provides compatibility macros.

**Q: Will the old API be removed?**
A: Eventually, yes. But there will be a deprecation period with warnings before removal.

**Q: What if I need to support other types?**
A: Currently only uint32_t and uint64_t are supported. If you need other types, please file an issue.

**Q: Is there a performance difference?**
A: No significant difference. The new API may be slightly faster due to better optimization opportunities.

**Q: Can I automate the migration?**
A: Partially. You can use sed/awk scripts for simple replacements, but manual review is recommended for correctness.

## Example Migration Script

Here's a simple script to help with mechanical changes (review output carefully!):

```bash
#!/bin/bash
# migrate_btree.sh - Helper script for btree migration
# Usage: ./migrate_btree.sh <file.c>

FILE=$1

# Backup original
cp "$FILE" "$FILE.bak"

# Update includes (you'll need to manually choose u32 or u64)
sed -i 's|#include "common/btree.h"|#include "common/btree/u32.h"  /* TODO: verify type */|g' "$FILE"

# Update struct declarations (you'll need to manually choose u32 or u64)
sed -i 's/struct btree /struct btree_u32 /g' "$FILE"

echo "Migration started for $FILE"
echo "Backup saved to $FILE.bak"
echo "TODO: Review changes and update function calls manually"
echo "TODO: Verify correct type (u32 vs u64) for each btree"
```

## Conclusion

The migration from the old macro-based API to the new function-based API is straightforward:

1. Identify your data types
2. Update includes
3. Update structure names
4. Replace macro calls with function calls
5. Test thoroughly

The new API provides better type safety, clearer code, and improved maintainability while maintaining the same performance characteristics.