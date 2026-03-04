# Tag Field Migration: uint32 → const char* (string)

## Overview

This document describes the refactoring plan to change the `tag` field in the balancer's allowed sources configuration from `uint32_t` to `const char*` (string type).

## Motivation

The tag field is used to identify and track statistics for allowed source entries. Changing from numeric to string tags provides:
- More descriptive and human-readable identifiers
- Easier integration with external systems that use string-based identifiers
- Better debugging and monitoring experience

## Semantics

- `tag = NULL`: No statistics tracking for this entry (equivalent to the old `tag = 0`)
- `tag = "some_string"`: Track packets matching this entry under the specified tag name

## Constraints

- **Maximum tag length: 240 characters** - Tags longer than 240 characters will be rejected during configuration validation
- This limit ensures counter names (format: `acl_%zu_%s`) fit within reasonable buffer sizes

## Files to Modify

### 1. C API Header
**File:** [`modules/balancer/controlplane/api/vs.h`](modules/balancer/controlplane/api/vs.h)

**Changes:**
- Line 348: Already has `const char *tag` - add proper documentation
- Lines 654-678: Change `struct allowed_sources_stats.tag` from `uint32_t` to `const char*`

```c
// In struct allowed_sources (line ~348):
/**
 * Tag identifier for tracking allowed source statistics.
 *
 * When non-NULL, enables per-tag statistics tracking for packets
 * matching this allowed source entry. Multiple allowed_sources entries
 * can share the same tag to aggregate statistics across different
 * network prefixes or port ranges.
 *
 * BEHAVIOR:
 * - tag = NULL: No statistics tracking for this entry (default)
 * - tag = "name": Track packets matching this entry under the specified tag
 *
 * STATISTICS:
 * - Tracked in allowed_sources_stats array in named_vs_stats
 * - Each unique tag gets its own statistics entry
 * - Counts total packets that passed allowed source filtering
 *
 * USE CASES:
 * - Track traffic from different customer networks separately
 * - Monitor access patterns by source category
 * - Aggregate statistics across multiple network ranges
 * - Identify which allowed sources are actively used
 *
 * EXAMPLES:
 * 1. Track internal vs external traffic:
 *    - Internal networks (10.0.0.0/8, 172.16.0.0/12): tag = "internal"
 *    - External networks (0.0.0.0/0): tag = "external"
 *
 * 2. Track per-customer traffic:
 *    - Customer A networks: tag = "customer_a"
 *    - Customer B networks: tag = "customer_b"
 *    - Customer C networks: tag = "customer_c"
 *
 * MEMORY MANAGEMENT:
 * - Caller owns the string memory
 * - String must remain valid for the lifetime of the configuration
 * - Balancer does not free this pointer
 */
const char *tag;

// In struct allowed_sources_stats (line ~654):
/**
 * Tag identifier matching allowed_sources.tag.
 *
 * This corresponds to the tag field in allowed_sources entries.
 * All entries with this tag contribute to these statistics.
 *
 * MEMORY MANAGEMENT:
 * - This is a heap-allocated copy of the original tag string
 * - Must be freed by caller (typically via balancer_stats_free())
 */
const char *tag;
```

### 2. C Controlplane Handler
**File:** [`modules/balancer/controlplane/handler/vs.c`](modules/balancer/controlplane/handler/vs.c)

**Add constant for max tag length:**
```c
#define MAX_TAG_LENGTH 240
```

**Add validation function:**
```c
static int
validate_tag(const char *tag) {
    if (tag == NULL) {
        return 0; // NULL is valid (means no tracking)
    }
    size_t len = strlen(tag);
    if (len > MAX_TAG_LENGTH) {
        NEW_ERROR("tag length %zu exceeds maximum %d", len, MAX_TAG_LENGTH);
        return -1;
    }
    return 0;
}
```

**Changes at line ~829:**
```c
// Before:
const char *rule_tag = config->allowed_src[allowed_src_idx].tag;
if (rule_tag != 0) {

// After:
const char *rule_tag = config->allowed_src[allowed_src_idx].tag;
if (rule_tag != NULL) {
```

**Add validation in setup_acl_rules (before using the tag):**
```c
if (validate_tag(rule_tag) != 0) {
    PUSH_ERROR("invalid tag at allowed_src index %u", allowed_src_idx);
    return -1;
}
```

The counter name format changes from `acl_%zu_%u` to `acl_%zu_%s`:
```c
sprintf(counter_name,
    "acl_%zu_%s",
    vs->registry_idx,
    rule_tag);
```

### 3. C Controlplane Stats
**File:** [`modules/balancer/controlplane/handler/stats.c`](modules/balancer/controlplane/handler/stats.c)

**Changes at line ~126:**
```c
// Before:
stats->tag = tag;

// After (need to duplicate the string):
stats->tag = strdup(tag);  // Or use appropriate memory allocation
```

**Changes to `parse_vs_acl_counter` function (line ~999):**
The function signature and implementation need to change to return a string tag instead of uint32:
```c
// Before:
ssize_t parse_vs_acl_counter(struct counter_handle *counter, uint32_t *tag);

// After:
ssize_t parse_vs_acl_counter(struct counter_handle *counter, const char **tag);
```

### 4. C Agent Config
**File:** [`modules/balancer/agent/config.c`](modules/balancer/agent/config.c)

**Changes at lines ~97 and ~460:**
Need to handle string duplication when cloning configurations:

```c
// In clone_allowed_src_to_relative (line ~97):
// Before:
entries[i].tag = src[i].tag;

// After:
if (src[i].tag != NULL) {
    size_t tag_len = strlen(src[i].tag) + 1;
    char *tag_copy = memory_balloc(mctx, tag_len);
    if (tag_copy == NULL) {
        // Handle error
        return -1;
    }
    memcpy(tag_copy, src[i].tag, tag_len);
    entries[i].tag = tag_copy;
    SET_OFFSET_OF(&entries[i].tag, entries[i].tag);
} else {
    entries[i].tag = NULL;
}

// In clone_allowed_src_from_relative (line ~460):
// Before:
entries[i].tag = src[i].tag;

// After:
if (src[i].tag != NULL) {
    const char *src_tag = ADDR_OF(&src[i].tag);
    entries[i].tag = strdup(src_tag);
} else {
    entries[i].tag = NULL;
}
```

Also need to update cleanup functions to free tag strings.

### 5. Protobuf Definition - Module
**File:** [`modules/balancer/agent/balancerpb/module.proto`](modules/balancer/agent/balancerpb/module.proto)

**Changes at line ~231:**
```protobuf
// Before:
uint32 tag = 3;

// After:
optional string tag = 3;
```

Update documentation:
```protobuf
// Tag identifier for tracking allowed source statistics.
//
// When specified (non-empty), enables per-tag statistics tracking for packets
// matching this allowed source entry. Multiple AllowedSources entries
// can share the same tag to aggregate statistics across different
// network prefixes or port ranges.
//
// BEHAVIOR:
// - tag not set or empty: No statistics tracking for this entry (default)
// - tag = "name": Track packets matching this entry under the specified tag
//
// STATISTICS:
// - Tracked in AllowedSourcesStats in NamedVsStats
// - Each unique non-empty tag gets its own statistics entry
// - Counts total packets that passed allowed source filtering
//
// USE CASES:
// - Track traffic from different customer networks separately
// - Monitor access patterns by source category
// - Aggregate statistics across multiple network ranges
// - Identify which allowed sources are actively used
//
// EXAMPLES:
// 1. Track internal vs external traffic:
//    - Internal networks (10.0.0.0/8, 172.16.0.0/12): tag = "internal"
//    - External networks (0.0.0.0/0): tag = "external"
//
// 2. Track per-customer traffic:
//    - Customer A networks: tag = "customer_a"
//    - Customer B networks: tag = "customer_b"
optional string tag = 3;
```

### 6. Protobuf Definition - Stats
**File:** [`modules/balancer/agent/balancerpb/stats.proto`](modules/balancer/agent/balancerpb/stats.proto)

**Changes at line ~228:**
```protobuf
// Before:
uint32 tag = 1;

// After:
string tag = 1;
```

### 7. Go FFI Types
**File:** [`modules/balancer/agent/go/ffi/types.go`](modules/balancer/agent/go/ffi/types.go)

**Changes at line ~30:**
```go
// Before:
type AllowedSources struct {
    Nets       []xnetip.NetWithMask
    PortRanges []PortRange
    Tag        uint32
}

// After:
type AllowedSources struct {
    Nets       []xnetip.NetWithMask
    PortRanges []PortRange
    Tag        string  // Empty string means no tracking
}
```

**Changes in NamedVsStats (line ~152):**
```go
// Before:
AllowedSources []struct {
    Tag    uint32
    Passes uint64
}

// After:
AllowedSources []struct {
    Tag    string
    Passes uint64
}
```

### 8. Go FFI Conversions
**File:** [`modules/balancer/agent/go/ffi/conversions.go`](modules/balancer/agent/go/ffi/conversions.go)

**Changes at line ~859:**
```go
// Before:
cAllowedSlice[i].tag = C.uint32_t(allowedSrc.Tag)

// After:
if allowedSrc.Tag != "" {
    cAllowedSlice[i].tag = C.CString(allowedSrc.Tag)
} else {
    cAllowedSlice[i].tag = nil
}
```

**Changes at line ~1033:**
```go
// Before:
config.AllowedSources[i].Tag = uint32(cAllowedSlice[i].tag)

// After:
if cAllowedSlice[i].tag != nil {
    config.AllowedSources[i].Tag = C.GoString(cAllowedSlice[i].tag)
} else {
    config.AllowedSources[i].Tag = ""
}
```

**Changes at line ~1320:**
```go
// Before:
stats.AllowedSources[i].Tag = uint32(cAllowedSourcesSlice[i].tag)

// After:
if cAllowedSourcesSlice[i].tag != nil {
    stats.AllowedSources[i].Tag = C.GoString(cAllowedSourcesSlice[i].tag)
} else {
    stats.AllowedSources[i].Tag = ""
}
```

Need to add memory cleanup for C strings in `freeCPacketHandlerConfig` and related functions.

### 9. Go Conversion Layer
**File:** [`modules/balancer/agent/go/conversion.go`](modules/balancer/agent/go/conversion.go)

**Changes at line ~456:**
```go
// Before:
allowedSrc = append(allowedSrc, ffi.AllowedSources{
    Nets:       nets,
    PortRanges: portRanges,
    Tag:        protoAllowedSrc.Tag,
})

// After:
tag := ""
if protoAllowedSrc.Tag != nil {
    tag = *protoAllowedSrc.Tag
}
allowedSrc = append(allowedSrc, ffi.AllowedSources{
    Nets:       nets,
    PortRanges: portRanges,
    Tag:        tag,
})
```

**Changes at line ~1299:**
```go
// Before:
allowedSrcs = append(allowedSrcs, &balancerpb.AllowedSources{
    Nets:  nets,
    Ports: protoPortRanges,
    Tag:   allowedSrc.Tag,
})

// After:
var tagPtr *string
if allowedSrc.Tag != "" {
    tagPtr = &allowedSrc.Tag
}
allowedSrcs = append(allowedSrcs, &balancerpb.AllowedSources{
    Nets:  nets,
    Ports: protoPortRanges,
    Tag:   tagPtr,
})
```

**Changes in stats conversion (line ~958):**
```go
// Before:
allowedSourcesStats = append(
    allowedSourcesStats,
    &balancerpb.AllowedSourcesStats{
        Tag:    stats.Vs[i].AllowedSources[j].Tag,
        Passes: stats.Vs[i].AllowedSources[j].Passes,
    },
)

// After:
allowedSourcesStats = append(
    allowedSourcesStats,
    &balancerpb.AllowedSourcesStats{
        Tag:    stats.Vs[i].AllowedSources[j].Tag,
        Passes: stats.Vs[i].AllowedSources[j].Passes,
    },
)
```

### 10. Go Tests
**File:** [`modules/balancer/tests/go/allowed_src_test.go`](modules/balancer/tests/go/allowed_src_test.go)

**Changes at multiple locations:**
```go
// Before:
Tag: 100,

// After:
Tag: "customer_100",

// Before:
Tag: 200,

// After:
Tag: "customer_200",

// Before:
Tag: 0, // Tag 0 means no tracking

// After:
Tag: "", // Empty tag means no tracking
```

Update tag stats checking:
```go
// Before:
tagStats[allowedSrc.Tag] = allowedSrc.Passes

// After (same, but Tag is now string):
tagStats[allowedSrc.Tag] = allowedSrc.Passes
```

## Validation

### Tag Length Validation (C Code)
Tags must be validated to ensure they don't exceed 240 characters. This validation should occur in:

1. **`modules/balancer/controlplane/handler/vs.c`** - During ACL rules setup (primary validation)
2. **`modules/balancer/agent/config.c`** - During configuration cloning (optional, as primary validation is in handler)

The validation function is already described in section 2 above.

## Memory Management Considerations

### C Code
1. **Configuration strings**: Caller owns the memory, must remain valid during configuration lifetime
2. **Cloned configurations**: Use `memory_balloc` for relative pointer contexts, `strdup` for normal heap
3. **Statistics strings**: Heap-allocated copies, freed by `balancer_stats_free()`
4. **Counter names**: Stack-allocated buffer (256 bytes), fits `acl_<idx>_<tag>` with 240-char tag limit

### Go Code
1. **FFI to C**: Use `C.CString()` which allocates memory - must be freed with `C.free()`
2. **C to FFI**: Use `C.GoString()` which copies the string - no special cleanup needed
3. **Protobuf**: Uses `*string` (optional) - nil means not set

## Testing Strategy

1. **Unit tests**: Update existing tests in `allowed_src_test.go` to use string tags
2. **Integration tests**: Verify stats collection with string tags
3. **Memory tests**: Run with address sanitizer to detect leaks
4. **Backward compatibility**: N/A - this is a breaking change

## Migration Notes

This is a **breaking change** that affects:
- Configuration files using numeric tags
- API clients using numeric tags
- Any external systems parsing tag values

Users must update their configurations to use string tags instead of numeric ones.