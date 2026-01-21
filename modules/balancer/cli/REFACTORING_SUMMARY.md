# Balancer CLI Refactoring Summary

## Overview
This document summarizes the key structural changes in the balancer protobuf interfaces and their impact on the CLI implementation.

## Key Protobuf Structure Changes

### 1. ShowConfigResponse Structure
**Old Structure:**
```
ShowConfigResponse {
  name: string
  module_config: ModuleConfig
  module_state_config: ModuleStateConfig
  buffered_real_updates: [RealUpdate]
}
```

**New Structure:**
```
ShowConfigResponse {
  config: BalancerConfig {
    packet_handler: PacketHandlerConfig (optional)
    state: StateConfig (optional)
  }
  buffered_real_updates: [RealUpdate]
}
```

**Key Changes:**
- No `name` field in response (name is in request only)
- Config wrapped in `BalancerConfig` with optional `packet_handler` and `state`
- `module_config` → `packet_handler`
- `module_state_config` → `state`
- PacketHandlerConfig.wlc moved to StateConfig.wlc

### 2. ShowInfoResponse Structure (Hierarchical)
**Old Structure (Flat):**
```
ShowInfoResponse {
  name: string
  info: {
    active_sessions: ValueWithTimestamp
    module: ModuleStats
    vs_info: [VsInfo]  // flat list
    real_info: [RealInfo]  // flat list
  }
}
```

**New Structure (Hierarchical):**
```
ShowInfoResponse {
  name: string
  info: BalancerInfo {
    active_sessions: uint64
    last_packet_timestamp: Timestamp
    vs: [VsInfo {
      id: VsIdentifier
      active_sessions: uint64
      last_packet_timestamp: Timestamp
      reals: [RealInfo {
        id: RealIdentifier
        active_sessions: uint64
        last_packet_timestamp: Timestamp
      }]
    }]
  }
}
```

**Key Changes:**
- Hierarchical structure: reals nested under their parent VS
- No separate `module` stats in info (moved to stats)
- `active_sessions` is now plain uint64, not ValueWithTimestamp
- Uses identifiers (VsIdentifier, RealIdentifier) instead of separate fields

### 3. ShowStatsResponse Structure (Hierarchical)
**Old Structure (Flat):**
```
ShowStatsResponse {
  name: string
  device: string
  pipeline: string
  function: string
  chain: string
  stats: {
    module: ModuleStats
    vs: [VsStats]  // flat list
    reals: [RealStats]  // flat list
  }
}
```

**New Structure (Hierarchical):**
```
ShowStatsResponse {
  ref: PacketHandlerRef {
    device: string (optional)
    pipeline: string (optional)
    function: string (optional)
    chain: string (optional)
  }
  stats: BalancerStats {
    l4: L4Stats
    icmpv4: IcmpStats
    icmpv6: IcmpStats
    common: CommonStats
    vs: [NamedVsStats {
      vs: VsIdentifier
      stats: VsStats
      reals: [NamedRealStats {
        real: RealIdentifier
        stats: RealStats
      }]
    }]
  }
}
```

**Key Changes:**
- Hierarchical structure: reals nested under their parent VS
- Topology info moved to `ref` field
- Module stats split into l4, icmpv4, icmpv6, common
- Uses NamedVsStats and NamedRealStats with identifiers

### 4. ShowSessionsResponse Structure
**Old Structure:**
```
ShowSessionsResponse {
  name: string
  sessions_info: [SessionInfo {
    client_addr: bytes
    client_port: uint32
    vs_addr: bytes
    vs_port: uint32
    real_addr: bytes
    real_port: uint32
    ...
  }]
}
```

**New Structure:**
```
ShowSessionsResponse {
  sessions: [SessionInfo {
    client_addr: Addr
    client_port: uint32
    vs_id: VsIdentifier
    real_id: RealIdentifier
    create_timestamp: Timestamp
    last_packet_timestamp: Timestamp
    timeout: Duration
  }]
}
```

**Key Changes:**
- `sessions_info` → `sessions`
- Uses Addr type instead of raw bytes
- Uses VsIdentifier and RealIdentifier instead of separate fields
- No `name` field in response

### 5. ListConfigsResponse Structure
**Old Structure:**
```
ListConfigsResponse {
  configs: [ShowConfigResponse]  // full configs
}
```

**New Structure:**
```
ListConfigsResponse {
  configs: [string]  // just names
}
```

**Key Changes:**
- Returns only config names, not full configurations
- Much more efficient for listing

### 6. RealUpdate Structure
**Old Structure:**
```
RealUpdate {
  virtual_ip: bytes
  port: uint32
  proto: TransportProto
  real_ip: bytes
  enable: bool
  weight: uint32
}
```

**New Structure:**
```
RealUpdate {
  real_id: RealIdentifier {
    vs: VsIdentifier
    real: RelativeRealIdentifier
  }
  enable: bool (optional)
  weight: uint32 (optional)
}
```

**Key Changes:**
- Uses hierarchical identifiers
- enable and weight are optional (only update if specified)

### 7. VirtualService Structure
**Old Structure:**
```
VirtualService {
  addr: bytes
  port: uint32
  proto: TransportProto
  scheduler: VsScheduler
  flags: VsFlags
  allowed_srcs: [Net]
  reals: [Real]
  peers: [bytes]
}
```

**New Structure:**
```
VirtualService {
  id: VsIdentifier {
    addr: Addr
    port: uint32
    proto: TransportProto
  }
  scheduler: VsScheduler
  allowed_srcs: [Net]
  reals: [Real]
  flags: VsFlags
  peers: [Addr]
}
```

**Key Changes:**
- addr, port, proto grouped into `id` field
- Uses Addr type instead of raw bytes

### 8. Real Structure
**Old Structure:**
```
Real {
  dst_addr: bytes
  port: uint32
  weight: uint32
  enabled: bool
  src_addr: bytes
  src_mask: bytes
}
```

**New Structure:**
```
Real {
  id: RelativeRealIdentifier {
    ip: Addr
    port: uint32
  }
  weight: uint32
  src_addr: Addr
  src_mask: Addr
}
```

**Key Changes:**
- dst_addr and port grouped into `id` field
- No `enabled` field in config (managed via RealUpdate)
- Uses Addr type instead of raw bytes

### 9. Scheduler Enum
**Old Values:**
- Wrr (Weighted Round Robin)
- Prr (Packet Round Robin)
- Wlc (Weighted Least Connection)

**New Values:**
- SOURCE_HASH (0)
- ROUND_ROBIN (1)

**Key Changes:**
- WLC is now a flag (VsFlags.wlc), not a scheduler
- Simplified to two core schedulers

## Implementation Strategy

### Phase 1: Fix output.rs (Current)
1. ✅ Add helper functions for Addr conversion
2. ⏳ Fix ShowConfigResponse access
3. Fix ShowInfoResponse access (hierarchical)
4. Fix ShowStatsResponse access (hierarchical)
5. Fix ShowSessionsResponse access
6. Fix ListConfigsResponse access

### Phase 2: Fix json_output.rs
1. Update ShowConfigResponse conversion
2. Update ShowInfoResponse conversion (hierarchical)
3. Update ShowStatsResponse conversion (hierarchical)
4. Update ShowSessionsResponse conversion
5. Update ListConfigsResponse conversion

### Phase 3: Update service.rs and cmd.rs
1. Rename RPC methods
2. Update request/response handling
3. Add Graph command support

### Phase 4: Testing
1. Compile and fix any remaining errors
2. Test with sample data
3. Verify hierarchical output displays correctly

## Helper Functions Added

```rust
// Convert Addr protobuf message to IP address
pub fn addr_to_ip(addr: &balancerpb::Addr) -> Result<IpAddr, String>

// Convert optional Addr protobuf message to IP address
pub fn opt_addr_to_ip(addr: &Option<balancerpb::Addr>) -> Result<IpAddr, String>
```

## Output Display Strategy

### ShowInfo (Hierarchical Display)
```
Balancer State Info
└── Config: my-balancer
    ├── Active Sessions: 1,234
    ├── Virtual Services (2)
    │   ├── [0] 10.0.0.1:80/TCP
    │   │   ├── Active Sessions: 500
    │   │   └── Reals
    │   │       ├── [0] 192.168.1.1 (250 sessions)
    │   │       └── [1] 192.168.1.2 (250 sessions)
    │   └── [1] 10.0.0.2:443/TCP
    │       ├── Active Sessions: 734
    │       └── Reals
    │           ├── [0] 192.168.2.1 (367 sessions)
    │           └── [1] 192.168.2.2 (367 sessions)
```

### ShowStats (Hierarchical Display)
```
Balancer Statistics
├── Module Stats
│   ├── L4: 1M pkts
│   ├── ICMPv4: 100K pkts
│   └── Common: 1.1M pkts
└── VS Stats (2)
    ├── [0] 10.0.0.1:80/TCP
    │   ├── Incoming: 500K pkts
    │   └── Reals
    │       ├── [0] 192.168.1.1: 250K pkts
    │       └── [1] 192.168.1.2: 250K pkts
    └── [1] 10.0.0.2:443/TCP
        ├── Incoming: 600K pkts
        └── Reals
            ├── [0] 192.168.2.1: 300K pkts
            └── [1] 192.168.2.2: 300K pkts
```

## Next Steps

1. Complete output.rs fixes for all response types
2. Update json_output.rs to match new structures
3. Update service.rs RPC method names
4. Add Graph command support
5. Test compilation and functionality