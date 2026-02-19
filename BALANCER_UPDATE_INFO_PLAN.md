# Balancer Update Info Feature - Architectural Plan

## Overview

This document outlines the implementation plan for returning metadata from balancer packet handler updates. The metadata tracks which components were reused (not recompiled) during configuration updates, providing visibility into the optimization behavior of the balancer.

## Metadata Structure

The update info contains:
1. **VS IPv4 Matcher Reused**: Boolean flag indicating if the IPv4 virtual service matcher filter was reused
2. **VS IPv6 Matcher Reused**: Boolean flag indicating if the IPv6 virtual service matcher filter was reused  
3. **VS ACL Reused List**: Array of virtual service identifiers for which ACL filters were reused

## Architecture Layers

```
┌─────────────────────────────────────────────────────────────┐
│                         CLI Layer                            │
│  - Display update metadata in human-readable format          │
│  - Show which filters were reused vs recompiled             │
└─────────────────────────────────────────────────────────────┘
                              ▲
                              │ gRPC
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      gRPC Service Layer                      │
│  - UpdateConfigResponse includes update_info field           │
│  - Converts Go structs to protobuf messages                 │
└─────────────────────────────────────────────────────────────┘
                              ▲
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                     Go Manager Layer                         │
│  - manager.Update() returns (*UpdateInfo, error)            │
│  - Logs reuse information at INFO level                     │
│  - Converts FFI structs to Go structs                       │
└─────────────────────────────────────────────────────────────┘
                              ▲
                              │ CGo/FFI
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                       FFI Layer (Go)                         │
│  - BalancerManager.Update() returns (*UpdateInfo, error)    │
│  - Converts C struct to Go struct                           │
│  - Frees C memory after conversion                          │
└─────────────────────────────────────────────────────────────┘
                              ▲
                              │ C API
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    C Manager Layer                           │
│  - balancer_manager_update() accepts update_info param      │
│  - Passes update_info to balancer_update_packet_handler()   │
└─────────────────────────────────────────────────────────────┘
                              ▲
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                  C Balancer Core Layer                       │
│  - balancer_update_packet_handler() accepts update_info     │
│  - Initializes update_info structure                        │
│  - Passes to packet_handler_setup()                         │
└─────────────────────────────────────────────────────────────┘
                              ▲
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                 C Packet Handler Layer                       │
│  - packet_handler_setup() tracks matcher reuse              │
│  - init_vs() allocates ACL reuse array                      │
│  - Populates update_info fields                             │
└─────────────────────────────────────────────────────────────┘
                              ▲
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                    C VS Layer                                │
│  - vs_init() accepts update_info parameter                  │
│  - setup_acl() tracks ACL filter reuse                      │
│  - Adds VS identifier to reuse list when ACL is reused      │
└─────────────────────────────────────────────────────────────┘
```

## Implementation Plan

### Phase 1: C Layer Foundation ✅ (Partially Complete)

#### 1.1 Define Update Info Structure ✅
**File**: `modules/balancer/controlplane/api/balancer.h`

```c
struct balancer_update_info {
    // Flags indicating matcher reuse
    int vs_ipv4_matcher_reused;  // 1 if reused, 0 if recompiled
    int vs_ipv6_matcher_reused;  // 1 if reused, 0 if recompiled
    
    // Array of VS identifiers with reused ACL
    size_t vs_acl_reused_count;
    struct vs_identifier *vs_acl_reused;  // Heap-allocated array
};
```

#### 1.2 Update Function Signatures ✅
- `balancer_update_packet_handler()` - accepts `update_info` parameter ✅
- `packet_handler_setup()` - accepts `update_info` parameter ✅
- `init_vs()` - accepts `update_info` parameter ✅
- `vs_init()` - accepts `update_info` parameter ⏳
- `setup_acl()` - accepts `update_info` parameter ⏳
- `balancer_manager_update()` - accepts `update_info` parameter ✅

#### 1.3 Track Matcher Reuse ✅
**File**: `modules/balancer/controlplane/handler/handler.c`

In `init_vs()`:
```c
// Track IPv4 matcher reuse
if (update_info != NULL) {
    update_info->vs_ipv4_matcher_reused = !need_ipv4_recompile;
    update_info->vs_ipv6_matcher_reused = !need_ipv6_recompile;
    
    // Allocate array for ACL reuse tracking
    update_info->vs_acl_reused = calloc(vs_count, sizeof(struct vs_identifier));
    update_info->vs_acl_reused_count = 0;
}
```

#### 1.4 Track ACL Reuse ⏳
**File**: `modules/balancer/controlplane/handler/vs.c`

In `setup_acl()`:
```c
static int
setup_acl(
    struct vs *vs,
    struct vs *prev_vs,
    struct memory_context *mctx,
    struct balancer_update_info *update_info
) {
    // Check if we can reuse ACL from previous VS
    if (prev_vs != NULL) {
        const struct filter_rule *prev_rules = ADDR_OF(&prev_vs->rules);
        const struct filter_rule *curr_rules = ADDR_OF(&vs->rules);
        
        if (rules_equal(curr_rules, vs->rules_count, prev_rules, prev_vs->rules_count)) {
            // Reuse ACL
            EQUATE_OFFSET(&vs->acl, &prev_vs->acl);
            
            // Track reuse in update_info
            if (update_info != NULL) {
                size_t idx = update_info->vs_acl_reused_count++;
                update_info->vs_acl_reused[idx] = vs->identifier;
            }
            
            return 0;
        }
    }
    
    // Need to create new ACL...
}
```

### Phase 2: Protobuf Definitions

#### 2.1 Add Update Info Message
**File**: `modules/balancer/agent/balancerpb/balancer.proto`

```protobuf
// Metadata about what was reused during configuration update
message UpdateInfo {
    // Whether the IPv4 virtual service matcher was reused (not recompiled)
    bool vs_ipv4_matcher_reused = 1;
    
    // Whether the IPv6 virtual service matcher was reused (not recompiled)
    bool vs_ipv6_matcher_reused = 2;
    
    // List of virtual services for which ACL filters were reused
    repeated VsIdentifier vs_acl_reused = 3;
}
```

#### 2.2 Update Response Message
**File**: `modules/balancer/agent/balancerpb/balancer.proto`

```protobuf
message UpdateConfigResponse {
    string name = 1;
    
    // Metadata about filter reuse during this update
    UpdateInfo update_info = 2;
}
```

### Phase 3: FFI Layer (C to Go)

#### 3.1 Define Go Struct
**File**: `modules/balancer/agent/go/ffi/types.go` (or similar)

```go
type UpdateInfo struct {
    VsIpv4MatcherReused bool
    VsIpv6MatcherReused bool
    VsAclReused         []VsIdentifier
}
```

#### 3.2 Implement C-to-Go Conversion
**File**: `modules/balancer/agent/go/ffi/manager.go`

```go
func (m *BalancerManager) Update(
    config *BalancerManagerConfig,
    now time.Time,
) (*UpdateInfo, error) {
    // ... existing code ...
    
    // Allocate C update_info structure
    var cUpdateInfo C.struct_balancer_update_info
    
    cNow := C.uint32_t(now.Unix())
    
    if C.balancer_manager_update(m.handle, cConfig, &cUpdateInfo, cNow) != 0 {
        cErr := C.balancer_manager_take_error(m.handle)
        errMsg := C.GoString(cErr)
        C.free(unsafe.Pointer(cErr))
        return nil, fmt.Errorf("failed to perform update: %s", errMsg)
    }
    
    // Convert C update_info to Go
    updateInfo := cToGo_UpdateInfo(&cUpdateInfo)
    
    // Free C allocations
    if cUpdateInfo.vs_acl_reused != nil {
        C.free(unsafe.Pointer(cUpdateInfo.vs_acl_reused))
    }
    
    return updateInfo, nil
}

func cToGo_UpdateInfo(c *C.struct_balancer_update_info) *UpdateInfo {
    info := &UpdateInfo{
        VsIpv4MatcherReused: c.vs_ipv4_matcher_reused != 0,
        VsIpv6MatcherReused: c.vs_ipv6_matcher_reused != 0,
        VsAclReused:         make([]VsIdentifier, c.vs_acl_reused_count),
    }
    
    if c.vs_acl_reused_count > 0 {
        // Convert C array to Go slice
        cArray := (*[1 << 30]C.struct_vs_identifier)(unsafe.Pointer(c.vs_acl_reused))[:c.vs_acl_reused_count:c.vs_acl_reused_count]
        
        for i := range info.VsAclReused {
            info.VsAclReused[i] = cToGo_VsIdentifier(&cArray[i])
        }
    }
    
    return info
}
```

### Phase 4: Go Manager Layer

#### 4.1 Update Manager.Update()
**File**: `modules/balancer/agent/go/manager.go`

```go
func (b *BalancerManager) Update(
    config *balancerpb.BalancerConfig,
    now time.Time,
) (*ffi.UpdateInfo, error) {
    b.mu.Lock()
    defer b.mu.Unlock()
    
    b.log.Debugw("updating balancer configuration")
    
    // ... existing config preparation code ...
    
    // Update via FFI (now returns update_info)
    updateInfo, err := b.handle.Update(managerConfig, now)
    if err != nil {
        b.log.Errorw("failed to update manager", "error", err)
        return nil, fmt.Errorf("failed to update manager: %w", err)
    }
    
    // Log reuse information
    b.log.Infow("balancer configuration updated successfully",
        "vs_ipv4_matcher_reused", updateInfo.VsIpv4MatcherReused,
        "vs_ipv6_matcher_reused", updateInfo.VsIpv6MatcherReused,
        "vs_acl_reused_count", len(updateInfo.VsAclReused))
    
    if len(updateInfo.VsAclReused) > 0 {
        b.log.Debugw("ACL filters reused for virtual services",
            "vs_identifiers", updateInfo.VsAclReused)
    }
    
    return updateInfo, nil
}
```

### Phase 5: gRPC Service Layer

#### 5.1 Update Service Implementation
**File**: `modules/balancer/agent/go/service.go` (or similar)

```go
func (s *BalancerService) UpdateConfig(
    ctx context.Context,
    req *balancerpb.UpdateConfigRequest,
) (*balancerpb.UpdateConfigResponse, error) {
    // ... existing code to get manager ...
    
    // Update configuration (now returns update_info)
    updateInfo, err := manager.Update(req.Config, time.Now())
    if err != nil {
        return nil, status.Errorf(codes.Internal, "failed to update: %v", err)
    }
    
    // Convert to protobuf
    pbUpdateInfo := convertUpdateInfoToProto(updateInfo)
    
    return &balancerpb.UpdateConfigResponse{
        Name:       req.Name,
        UpdateInfo: pbUpdateInfo,
    }, nil
}

func convertUpdateInfoToProto(info *ffi.UpdateInfo) *balancerpb.UpdateInfo {
    pbInfo := &balancerpb.UpdateInfo{
        VsIpv4MatcherReused: info.VsIpv4MatcherReused,
        VsIpv6MatcherReused: info.VsIpv6MatcherReused,
        VsAclReused:         make([]*balancerpb.VsIdentifier, len(info.VsAclReused)),
    }
    
    for i, vsId := range info.VsAclReused {
        pbInfo.VsAclReused[i] = convertVsIdentifierToProto(&vsId)
    }
    
    return pbInfo
}
```

### Phase 6: CLI Layer

#### 6.1 Display Update Info
**File**: `cli/modules/balancer/src/update.rs` (or similar)

```rust
fn display_update_response(resp: &UpdateConfigResponse) {
    println!("✓ Configuration updated for balancer: {}", resp.name);
    
    if let Some(info) = &resp.update_info {
        println!("\nFilter Reuse Summary:");
        println!("  IPv4 VS Matcher: {}", 
            if info.vs_ipv4_matcher_reused { "✓ Reused" } else { "✗ Recompiled" });
        println!("  IPv6 VS Matcher: {}", 
            if info.vs_ipv6_matcher_reused { "✓ Reused" } else { "✗ Recompiled" });
        
        if !info.vs_acl_reused.is_empty() {
            println!("\n  ACL Filters Reused ({} virtual services):", info.vs_acl_reused.len());
            for vs_id in &info.vs_acl_reused {
                println!("    - {}:{} ({})", 
                    format_addr(&vs_id.addr),
                    vs_id.port,
                    format_proto(vs_id.proto));
            }
        } else {
            println!("  ACL Filters: All recompiled");
        }
    }
}
```

## Testing Strategy

### Unit Tests

1. **C Layer Tests**
   - Test `setup_acl()` correctly identifies when rules are equal
   - Test `setup_acl()` populates `update_info` when ACL is reused
   - Test matcher reuse tracking in `init_vs()`

2. **FFI Tests**
   - Test C-to-Go conversion of `UpdateInfo`
   - Test memory management (no leaks)

3. **Go Manager Tests**
   - Test `Update()` returns correct metadata
   - Test logging of reuse information

### Integration Tests

1. **Update Scenarios**
   - Update with no changes → all filters reused
   - Update with VS changes → matchers recompiled, some ACLs reused
   - Update with ACL changes → specific ACLs recompiled
   - Update adding new VS → matchers recompiled, existing ACLs may be reused

2. **gRPC Tests**
   - Test `UpdateConfig` RPC returns metadata
   - Test protobuf serialization/deserialization

3. **CLI Tests**
   - Test metadata display formatting
   - Test with various reuse scenarios

## Performance Considerations

1. **Memory Allocation**
   - `vs_acl_reused` array is allocated once per update
   - Size is bounded by number of virtual services
   - Freed immediately after conversion to Go

2. **Comparison Overhead**
   - Rule comparison is O(n) where n = number of rules
   - Only performed when previous VS exists
   - Negligible compared to filter compilation

3. **Logging Impact**
   - Metadata logging at INFO level
   - Detailed VS list at DEBUG level
   - No performance impact in production

## Migration Path

1. **Phase 1**: C layer implementation (current)
2. **Phase 2**: Protobuf definitions
3. **Phase 3**: FFI layer
4. **Phase 4**: Go manager layer
5. **Phase 5**: gRPC service
6. **Phase 6**: CLI display

Each phase can be tested independently before proceeding to the next.

## Open Questions

1. Should we track individual rule changes within ACL filters?
2. Should we add metrics/counters for reuse rates?
3. Should we expose this information via a separate Info/Stats API?

## References

- Filter compiler: `filter/compiler.h`
- VS implementation: `modules/balancer/controlplane/handler/vs.c`
- Manager implementation: `modules/balancer/agent/manager.c`
- gRPC service: `modules/balancer/agent/balancerpb/balancer.proto`