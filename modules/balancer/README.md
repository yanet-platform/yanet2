# Balancer Module

The balancer module provides Layer 4 (TCP/UDP) load balancing functionality for the YANET platform. It distributes incoming traffic across multiple backend servers (real servers) while maintaining session affinity and providing advanced features like dynamic weight adjustment and ICMP error handling.

## Overview

The balancer operates as a packet handler in the YANET dataplane pipeline, intercepting traffic destined for configured virtual services and forwarding it to backend real servers using IP-in-IP (IPIP) or GRE encapsulation.

### Key Features

- **Layer 4 Load Balancing**: Distributes TCP and UDP traffic across multiple backend servers
- **Multiple Scheduling Algorithms**: 
  - Source Hash (session affinity based on client IP/port)
  - Weighted Round Robin (even distribution with weight support)
- **Session Tracking**: Maintains connection state to ensure packets of the same flow reach the same backend
- **Dynamic Weight Adjustment**: Weighted Least Connection (WLC) algorithm automatically adjusts real server weights based on active session counts
- **Encapsulation Support**: IPIP and GRE tunneling for forwarding traffic to real servers
- **ICMP Handling**: Processes ICMP echo requests and error messages, with support for multi-balancer broadcasting
- **Source Address Filtering**: Optional restriction of traffic to specific client networks
- **Pure L3 Mode**: Load balance all traffic to an IP address regardless of port
- **One Packet Scheduler (OPS)**: Stateless per-packet scheduling without session tracking
- **Automatic Session Table Resizing**: Dynamically grows session table capacity based on load
- **Real Server Management**: Runtime updates to real server weights and enabled/disabled state

## Architecture

### Components

1. **Packet Handler** ([`dataplane/`](dataplane/)): Fast-path packet processing in the dataplane
   - Virtual service matching and selection
   - Real server scheduling (SOURCE_HASH, ROUND_ROBIN)
   - Session table lookup and creation
   - Packet encapsulation (IPIP/GRE)
   - ICMP processing and broadcasting
   - TCP MSS adjustment

2. **Control Plane** ([`controlplane/`](controlplane/)): Configuration management and state synchronization
   - Configuration validation and updates
   - Real server property updates (buffered and immediate)
   - Session table management and resizing
   - Periodic refresh for statistics and WLC
   - State synchronization with dataplane

3. **Agent Service** ([`agent/`](agent/)): gRPC API for management operations
   - Configuration CRUD operations
   - Real server updates with buffering support
   - Statistics and runtime information queries
   - Active session inspection
   - Topology graph visualization

## Configuration

### Virtual Service

A virtual service defines a load-balanced endpoint that distributes traffic across multiple real servers:

```protobuf
message VirtualService {
  VsIdentifier id = 1;              // IP, port, protocol
  VsScheduler scheduler = 2;         // SOURCE_HASH or ROUND_ROBIN
  repeated Net allowed_srcs = 3;     // Optional source filtering
  repeated Real reals = 4;           // Backend servers
  VsFlags flags = 5;                 // Feature flags
  repeated Addr peers = 6;           // Peer balancer addresses
}
```

**Virtual Service Identifier** uniquely identifies a service by:
- IP address (IPv4 or IPv6)
- Port number (0 for pure L3 mode)
- Transport protocol (TCP or UDP)

### Real Server

A real server represents a backend that handles forwarded traffic:

```protobuf
message Real {
  RelativeRealIdentifier id = 1;    // IP address
  uint32 weight = 2;                 // Scheduling weight (1-65535)
  Addr src_addr = 3;                 // Encapsulation source address
  Addr src_mask = 4;                 // Encapsulation source mask
}
```

**Weight** determines traffic distribution:
- **SOURCE_HASH**: Higher weight = more hash buckets = more traffic
- **ROUND_ROBIN**: Weight determines consecutive connection count

### Feature Flags

Control virtual service behavior:

- **`gre`**: Use GRE encapsulation instead of IPIP
- **`fix_mss`**: Adjust TCP MSS to account for encapsulation overhead
- **`ops`**: One Packet Scheduler mode (stateless, no session tracking)
- **`pure_l3`**: Match all traffic to IP regardless of port (port must be 0)
- **`wlc`**: Enable Weighted Least Connection dynamic weight adjustment

### Session Timeouts

Configure session lifetime based on protocol and TCP state:

```protobuf
message SessionsTimeouts {
  uint32 tcp_syn_ack = 1;   // SYN-ACK state timeout
  uint32 tcp_syn = 2;       // SYN state timeout
  uint32 tcp_fin = 3;       // FIN state timeout
  uint32 tcp = 4;           // Established state timeout
  uint32 udp = 5;           // UDP session timeout
  uint32 default = 6;       // Default timeout
}
```

### State Configuration

Controls session table and periodic operations:

```protobuf
message StateConfig {
  uint64 session_table_capacity = 1;           // Max concurrent sessions
  float session_table_max_load_factor = 2;     // Auto-resize threshold (0.7-0.9)
  WlcConfig wlc = 3;                           // WLC algorithm parameters
  google.protobuf.Duration refresh_period = 4; // Periodic refresh interval
}
```

**Refresh Period** enables periodic operations:
- Session table scanning and statistics updates
- Automatic session table resizing when load exceeds threshold
- WLC weight adjustment (if enabled)
- Set to 0 to disable periodic operations

### Weighted Least Connection (WLC)

WLC dynamically adjusts real server weights based on active session distribution:

```protobuf
message WlcConfig {
  uint64 power = 1;        // Adjustment aggressiveness (1-16)
  uint32 max_weight = 2;   // Maximum effective weight cap
}
```

**Algorithm**:
```
ratio = (real_sessions * total_weight) / (total_sessions * real_weight)
wlc_factor = max(1.0, power * (1.0 - ratio))
effective_weight = min(real_weight * wlc_factor, max_weight)
```

**Requirements** for WLC:
1. Set `VsFlags.wlc = true` for the virtual service
2. Configure `StateConfig.refresh_period` to non-zero value
3. Configure `StateConfig.session_table_max_load_factor`
4. Configure `StateConfig.wlc` with power and max_weight

## Scheduling Algorithms

### SOURCE_HASH

Selects real servers based on hash of client source IP and port:
- Provides session affinity (same client → same real)
- Weight affects hash space distribution
- Best for stateful applications requiring client stickiness

### ROUND_ROBIN

Maintains monotonic counter and selects reals consecutively:
- Each real receives `weight` consecutive connections
- More even distribution across all reals
- Best for stateless applications

## Packet Processing Flow

1. **Ingress**: Packet arrives at balancer
2. **Decapsulation**: If destination matches decap address, remove outer IP header
3. **Virtual Service Selection**: Match packet to configured virtual service
4. **Source Filtering**: Check if source address is allowed (if configured)
5. **Session Lookup**: Check if session exists in session table
6. **Real Selection**: If new session, select real using scheduler
7. **Session Creation**: Create new session entry (unless OPS mode)
8. **Encapsulation**: Wrap packet in IPIP or GRE tunnel
9. **MSS Adjustment**: Fix TCP MSS if enabled
10. **Forwarding**: Send packet to selected real server

## ICMP Handling

The balancer processes two types of ICMP messages:

### ICMP Echo (Ping)
- Responds to pings for virtual service addresses
- Useful for health checking and monitoring

### ICMP Error Messages
- Processes errors related to forwarded sessions
- Validates error against known sessions
- Forwards error to appropriate real server
- Broadcasts to peer balancers in multi-balancer setups

## Statistics

The balancer tracks comprehensive statistics:

### L4 Statistics
- Incoming/outgoing packet counts
- Virtual service selection failures
- Real server selection failures
- Invalid packet counts

### ICMP Statistics
- Echo requests and responses
- Error message processing
- Peer broadcasting metrics
- Packet clone operations

### Per-Virtual-Service Statistics
- Packet and byte counts
- Session creation counts
- Source filtering rejections
- Session table overflow events
- Real server availability issues

### Per-Real-Server Statistics
- Packet and byte counts forwarded
- Session creation counts
- OPS packet counts
- ICMP error forwarding

## API Operations

The balancer provides a gRPC service with the following operations:

### Configuration Management
- **`UpdateConfig`**: Create or update balancer configuration
- **`ShowConfig`**: Retrieve current configuration
- **`ListConfigs`**: List all balancer instances

### Real Server Management
- **`UpdateReals`**: Update real server weights and enabled state
  - Supports immediate or buffered updates
  - Buffered updates applied atomically on flush
- **`FlushRealUpdates`**: Apply all buffered real server updates

### Monitoring
- **`ShowStats`**: Retrieve packet processing statistics
- **`ShowInfo`**: Get runtime information (session counts, timestamps)
- **`ShowSessions`**: List all active sessions
- **`ShowGraph`**: Visualize balancer topology

## Session Management

Sessions are tracked in a hash table with the following properties:

- **Key**: Client IP, client port, virtual service identifier
- **Value**: Assigned real server, timestamps, timeout
- **Capacity**: Configurable, automatically resized when load exceeds threshold
- **Timeout**: Based on protocol and TCP state
- **Cleanup**: Expired sessions removed during periodic refresh

## Multi-Balancer Support

Multiple balancer instances can coordinate for high availability:

- **Peer Configuration**: List peer balancer addresses in virtual service
- **ICMP Broadcasting**: Error messages broadcasted to all peers
- **Coordinated Processing**: Ensures all balancers handle errors consistently

## Performance Considerations

### Session Table Sizing
- Larger capacity = more memory, supports more concurrent connections
- Auto-resize prevents overflow but causes temporary performance impact
- Set initial capacity based on expected peak load

### Refresh Period
- Shorter period = more responsive, higher CPU overhead
- Longer period = less overhead, slower response to changes
- Typical values: 5-30 seconds for dynamic workloads

### WLC Configuration
- Higher power = more aggressive adjustment, faster response
- Lower power = gentler adjustment, more stable weights
- Balance between responsiveness and stability

### Scheduler Selection
- SOURCE_HASH: Better for stateful apps, may have uneven distribution
- ROUND_ROBIN: Better for stateless apps, more even distribution

## Example Configuration

```protobuf
BalancerConfig {
  packet_handler: {
    vs: [
      {
        id: { addr: "10.0.0.1", port: 80, proto: TCP }
        scheduler: SOURCE_HASH
        reals: [
          { id: { ip: "192.168.1.10" }, weight: 100 }
          { id: { ip: "192.168.1.11" }, weight: 100 }
          { id: { ip: "192.168.1.12" }, weight: 50 }
        ]
        flags: { fix_mss: true, wlc: true }
      }
    ]
    source_address_v4: "10.0.0.100"
    source_address_v6: "2001:db8::100"
    sessions_timeouts: {
      tcp_syn: 30
      tcp_syn_ack: 30
      tcp_fin: 30
      tcp: 300
      udp: 60
      default: 60
    }
  }
  state: {
    session_table_capacity: 1000000
    session_table_max_load_factor: 0.8
    wlc: { power: 4, max_weight: 500 }
    refresh_period: { seconds: 10 }
  }
}
```

## See Also

- [Protobuf API Documentation](agent/balancerpb/) - Complete API reference
- [Dataplane Implementation](dataplane/) - Fast-path packet processing
- [Control Plane](controlplane/) - Configuration and state management