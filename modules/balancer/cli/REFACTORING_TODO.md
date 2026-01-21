# Balancer CLI Refactoring TODO

## Current Status

### Completed
- ✅ Added helper functions (`addr_to_ip`, `opt_addr_to_ip`) to entities.rs
- ✅ Fixed ShowConfigResponse structure access (partially)
- ✅ Fixed ListConfigsResponse structure access
- ✅ Created comprehensive documentation (REFACTORING_SUMMARY.md)

### In Progress - output.rs

The file has been partially updated but needs completion. Here are the remaining issues:

#### 1. Remove Duplicate print_show_info_table (line 707+)
There are two definitions of `print_show_info_table`. Remove the old one that references:
- `info.module` (doesn't exist in new structure)
- `info.vs_info` (should be `info.vs`)
- `info.real_info` (reals are now nested under VS)
- `info.active_sessions.as_ref().map(|a| a.value)` (now plain u64)

#### 2. Fix print_show_info_table to use hierarchical structure
The new structure is:
```rust
BalancerInfo {
    active_sessions: u64,  // plain u64, not Option<ValueWithTimestamp>
    last_packet_timestamp: Timestamp,
    vs: Vec<VsInfo {
        id: VsIdentifier,
        active_sessions: u64,
        last_packet_timestamp: Timestamp,
        reals: Vec<RealInfo {  // nested under VS!
            id: RealIdentifier,
            active_sessions: u64,
            last_packet_timestamp: Timestamp,
        }>
    }>
}
```

Display should show reals nested under their parent VS.

#### 3. Fix print_show_stats_tree
Current errors:
- `response.name` → doesn't exist (no name in ShowStatsResponse)
- `response.device/pipeline/function/chain` → now in `response.ref` (PacketHandlerRef)
- `stats.module` → split into `stats.l4`, `stats.icmpv4`, `stats.icmpv6`, `stats.common`
- `stats.reals` → doesn't exist at top level, reals are nested under `stats.vs[].reals`

New structure:
```rust
ShowStatsResponse {
    ref: PacketHandlerRef {
        device: Option<String>,
        pipeline: Option<String>,
        function: Option<String>,
        chain: Option<String>,
    },
    stats: BalancerStats {
        l4: L4Stats,
        icmpv4: IcmpStats,
        icmpv6: IcmpStats,
        common: CommonStats,
        vs: Vec<NamedVsStats {
            vs: VsIdentifier,
            stats: VsStats,
            reals: Vec<NamedRealStats {  // nested!
                real: RealIdentifier,
                stats: RealStats,
            }>
        }>
    }
}
```

#### 4. Fix print_show_stats_table
Same issues as print_show_stats_tree.

#### 5. Fix print_show_sessions
Update to use new SessionInfo structure:
```rust
SessionInfo {
    client_addr: Addr,
    client_port: u32,
    vs_id: VsIdentifier,  // not separate vs_addr, vs_port fields
    real_id: RealIdentifier,  // not separate real_addr, real_port fields
    create_timestamp: Timestamp,
    last_packet_timestamp: Timestamp,
    timeout: Duration,
}
```

### Remaining Files

#### json_output.rs
Needs complete rewrite of conversion functions:

1. **convert_show_config** - Update for new structure:
   - No `response.name` field
   - `response.config.packet_handler` (optional)
   - `response.config.state` (optional)
   - RealUpdate uses RealIdentifier structure

2. **convert_show_info** (rename from convert_state_info):
   - Hierarchical structure
   - No module stats in info
   - Reals nested under VS

3. **convert_show_stats** (rename from convert_config_stats):
   - Use `response.ref` for topology info
   - Module stats split into l4/icmpv4/icmpv6/common
   - Hierarchical VS/Real structure

4. **convert_show_sessions** (rename from convert_sessions_info):
   - Use identifiers instead of separate fields
   - `response.sessions` not `response.sessions_info`

5. **convert_list_configs**:
   - Returns list of strings, not full configs

6. **Scheduler enum conversion**:
   - Change from Wrr/Prr/Wlc to SourceHash/RoundRobin

#### service.rs
Update RPC method names:
- `state_info()` → `show_info()`
- `config_stats()` → `show_stats()`
- `sessions_info()` → `show_sessions()`

Add new method:
- `show_graph()`

#### cmd.rs
Update command structures and add Graph command.

## Quick Reference: Key Changes

### Scheduler Enum
```rust
// Old
Wrr, Prr, Wlc

// New  
SOURCE_HASH (0), ROUND_ROBIN (1)
// WLC is now a flag in VsFlags
```

### Identifiers
```rust
// Old: separate fields
vs_ip, vs_port, vs_proto
real_ip, real_port

// New: structured identifiers
VsIdentifier { addr: Addr, port: u32, proto: TransportProto }
RealIdentifier { 
    vs: VsIdentifier,
    real: RelativeRealIdentifier { ip: Addr, port: u32 }
}
```

### Hierarchical Structure
```
Old (Flat):
- info.vs_info: [VsInfo]
- info.real_info: [RealInfo]

New (Hierarchical):
- info.vs: [VsInfo { reals: [RealInfo] }]
```

## Implementation Order

1. ✅ Complete output.rs ShowInfo functions
2. ✅ Complete output.rs ShowStats functions  
3. ✅ Complete output.rs ShowSessions functions
4. Update json_output.rs (all conversion functions)
5. Update service.rs (RPC method names)
6. Update cmd.rs (add Graph command)
7. Test compilation
8. Test with sample data

## Testing Strategy

After each file is updated:
1. Run `cargo check` to verify syntax
2. Fix any remaining type errors
3. Run `cargo build` to ensure compilation
4. Test with mock data if available

## Notes

- The REFACTORING_SUMMARY.md file contains detailed examples of all structure changes
- All changes maintain backward compatibility in YAML config format
- The hierarchical display improves readability by showing reals under their parent VS