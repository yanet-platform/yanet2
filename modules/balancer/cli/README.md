# yanet-balancer CLI

Command-line interface for managing the YANET balancer module.

## Installation

```bash
cd modules/balancer/cli
make build
sudo make install
```

The binary will be installed as `yanet-balancer` in `/usr/local/bin` by default.

## Usage

```bash
yanet-balancer [OPTIONS] <COMMAND>
```

### Global Options

- `--endpoint <URL>` - gRPC endpoint (default: `grpc://[::1]:8080`)
- `-v, -vv, -vvv` - Increase verbosity (info, debug, trace)
- `--help` - Show help information
- `--version` - Show version information

### Commands

#### 1. update

Update balancer configuration from a YAML file.

```bash
yanet-balancer update \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  --config <PATH_TO_YAML>
```

**Example:**
```bash
yanet-balancer update \
  --name my-balancer \
  --instance 0 \
  --config example-config.yaml
```

See [`example-config.yaml`](example-config.yaml) for configuration file format.

#### 2. reals enable

Enable a real server (buffered).

```bash
yanet-balancer reals enable \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  --virtual-ip <VIP> \
  --proto <tcp|udp> \
  --virtual-port <PORT> \
  --real-ip <REAL_IP> \
  [--weight <WEIGHT>]
```

**Example:**
```bash
yanet-balancer reals enable \
  --name my-balancer \
  --virtual-ip 192.0.2.1 \
  --proto tcp \
  --virtual-port 80 \
  --real-ip 10.1.1.1 \
  --weight 200
```

#### 3. reals disable

Disable a real server (buffered).

```bash
yanet-balancer reals disable \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  --virtual-ip <VIP> \
  --proto <tcp|udp> \
  --virtual-port <PORT> \
  --real-ip <REAL_IP>
```

**Example:**
```bash
yanet-balancer reals disable \
  --name my-balancer \
  --virtual-ip 192.0.2.1 \
  --proto tcp \
  --virtual-port 80 \
  --real-ip 10.1.1.2
```

#### 4. reals flush

Flush buffered real server updates.

```bash
yanet-balancer reals flush \
  --name <CONFIG_NAME> \
  --instance <INSTANCE>
```

**Example:**
```bash
yanet-balancer reals flush --name my-balancer --instance 0
```

#### 5. config

Show balancer configuration.

```bash
yanet-balancer config \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  [--format <table|json|tree>]
```

**Examples:**
```bash
# Show as table (default)
yanet-balancer config --name my-balancer

# Show as JSON
yanet-balancer config --name my-balancer --format json

# Show as tree
yanet-balancer config --name my-balancer --format tree
```

#### 6. list

List all balancer configurations.

```bash
yanet-balancer list [--format <table|json|tree>]
```

**Example:**
```bash
yanet-balancer list --format table
```

#### 7. stats

Show configuration statistics.

```bash
yanet-balancer stats \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  --device <DEVICE> \
  --pipeline <PIPELINE> \
  --function <FUNCTION> \
  --chain <CHAIN> \
  [--format <table|json|tree>]
```

**Example:**
```bash
yanet-balancer stats \
  --name my-balancer \
  --instance 0 \
  --device eth0 \
  --pipeline main \
  --function balancer \
  --chain default \
  --format table
```

#### 8. state

Show balancer state information (active sessions, VS info, real info).

```bash
yanet-balancer state \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  [--format <table|json|tree>]
```

**Example:**
```bash
yanet-balancer state --name my-balancer --format table
```

#### 9. sessions

Show active sessions information.

```bash
yanet-balancer sessions \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  [--format <table|json|tree>]
```

**Example:**
```bash
yanet-balancer sessions --name my-balancer --format table
```

## Output Formats

All display commands support three output formats:

- **table** (default) - Human-readable formatted tables with colored output
- **json** - Machine-readable JSON format (full gRPC response)
- **tree** - Hierarchical tree structure (full gRPC response)

## Configuration File Format

The configuration file is in YAML format with two main sections:

### packet_handler

Packet processing configuration containing:

- `vs` - List of virtual services
  - `addr` - Virtual IP address (IPv4 or IPv6)
  - `port` - Port number (0 for pure_l3 mode)
  - `proto` - Protocol: `TCP`, `tcp`, `UDP`, or `udp`
  - `scheduler` - Scheduler algorithm:
    - `SOURCE_HASH`, `source_hash`, `SH`, `sh` - Source hash scheduling
    - `ROUND_ROBIN`, `round_robin`, `RR`, `rr` - Round-robin scheduling
  - `flags` - Service flags:
    - `gre` - GRE encapsulation
    - `fix_mss` - TCP MSS fixing
    - `ops` - One Packet Scheduler (no session tracking)
    - `pure_l3` - Match all ports (port must be 0)
    - `wlc` - Enable dynamic weight adjustment
  - `allowed_srcs` - Source access control (see [Source Port Filtering](#source-port-filtering))
  - `reals` - List of real servers:
    - `ip` - Real server IP address
    - `port` - Port (reserved for future use, currently must be 0)
    - `weight` - Server weight for scheduling
    - `src_addr` - Source address for forwarding
    - `src_mask` - Source mask
  - `peers` - List of peer balancer IPs for session synchronization

- `source_address_v4` - IPv4 source address for encapsulation
- `source_address_v6` - IPv6 source address for encapsulation
- `sessions_timeouts` - Session timeout configuration (in seconds):
  - `tcp_syn_ack` - TCP SYN-ACK timeout
  - `tcp_syn` - TCP SYN timeout
  - `tcp_fin` - TCP FIN timeout
  - `tcp` - Established TCP connection timeout
  - `udp` - UDP session timeout
  - `default` - Default timeout for other protocols

### state

State management configuration:

- `session_table` - Session table configuration:
  - `capacity` - Maximum concurrent sessions
  - `max_load_factor` - Trigger resize at this load factor (0.0-1.0)
- `wlc` - WLC (Weighted Least Connections) configuration:
  - `power` - Adjustment aggressiveness
  - `max_weight` - Maximum weight after adjustment
- `refresh_period_ms` - Periodic refresh interval in milliseconds (0 to disable)

### Source Port Filtering

The `allowed_srcs` field provides fine-grained access control based on source IP addresses and optionally source ports. This feature is useful for:

- Restricting access to specific client networks
- Limiting connections to known source port ranges
- Implementing security policies based on ephemeral port usage
- Controlling access from specific applications or services

#### Format Options

**Simple Format (Backward Compatible)**

A simple string specifying the network in CIDR or netmask notation. All source ports are allowed.

```yaml
allowed_srcs:
  - "10.0.0.0/8"                    # CIDR notation
  - "172.16.0.0/255.240.0.0"        # Netmask notation
  - "192.168.1.0/24"                # Single /24 network
  - "203.0.113.42/32"               # Single host
  - "2001:db8::/32"                 # IPv6 network
```

**Structured Format with Port Filtering**

An object with `network` and optional `ports` fields for fine-grained control:

```yaml
allowed_srcs:
  # Network with single source port
  - network: "10.0.0.0/8"
    ports: "443"
  
  # Network with port range
  - network: "172.16.0.0/12"
    ports: "1024-65535"
  
  # Network with multiple ports and ranges
  - network: "192.168.0.0/16"
    ports: "80,443,8000-9000,3000-3010"
  
  # Netmask notation with ports
  - network: "198.51.100.0/255.255.255.0"
    ports: "22,3389,5900-5910"
```

**Mixed Format**

You can mix simple and structured formats in the same `allowed_srcs` list:

```yaml
allowed_srcs:
  # Simple format - all ports allowed
  - "10.0.0.0/8"
  
  # Structured with ports
  - network: "172.16.0.0/12"
    ports: "443,8443"
  
  # Another simple entry
  - "192.168.0.0/16"
  
  # Structured with port range
  - network: "203.0.113.0/24"
    ports: "1024-65535"
```

#### Port Specification Format

The `ports` field accepts a comma-separated list of ports and port ranges:

- **Single port**: `"80"`, `"443"`, `"8080"`
- **Port range**: `"1024-65535"`, `"8000-9000"`
- **Multiple entries**: `"80,443,8000-9000,3000-3010"`

**Rules:**
- Port numbers must be in range 1-65535
- In ranges, the `from` port must be ≤ `to` port
- Whitespace around commas and hyphens is ignored
- Empty or missing `ports` field means all ports are allowed

#### Access Control Semantics

- **Empty list** (`allowed_srcs: []`) - **DENY ALL** traffic (useful for maintenance mode)
- **Allow all IPv4**: `["0.0.0.0/0"]`
- **Allow all IPv6**: `["::/0"]`
- **Multiple entries** - Traffic is allowed if it matches ANY entry (OR logic)
- **Port filtering** - When ports are specified, BOTH network AND port must match

#### Examples

**Example 1: Restrict to internal networks only**
```yaml
allowed_srcs:
  - "10.0.0.0/8"
  - "172.16.0.0/12"
  - "192.168.0.0/16"
```

**Example 2: Allow specific networks with ephemeral ports only**
```yaml
allowed_srcs:
  - network: "10.0.0.0/8"
    ports: "1024-65535"  # Only ephemeral ports
  - network: "172.16.0.0/12"
    ports: "32768-65535"  # Linux default ephemeral range
```

**Example 3: Mixed access - some networks unrestricted, others port-limited**
```yaml
allowed_srcs:
  # Trusted network - all ports
  - "10.0.0.0/8"
  
  # DMZ network - only HTTPS source ports
  - network: "172.16.0.0/12"
    ports: "443,8443"
  
  # External network - only high ports
  - network: "203.0.113.0/24"
    ports: "1024-65535"
```

**Example 4: Service-specific restrictions**
```yaml
allowed_srcs:
  # Allow SSH clients (typically use high ports)
  - network: "192.168.1.0/24"
    ports: "1024-65535"
  
  # Allow RDP clients
  - network: "192.168.2.0/24"
    ports: "3389"
  
  # Allow VNC clients
  - network: "192.168.3.0/24"
    ports: "5900-5910"
```

See [`example-config.yaml`](example-config.yaml) for a complete example with all features.

## Workflow Example

```bash
# 1. Update configuration
yanet-balancer update \
  --name my-balancer \
  --config config.yaml

# 2. Check configuration
yanet-balancer config --name my-balancer

# 3. Disable a real server (buffered)
yanet-balancer reals disable \
  --name my-balancer \
  --virtual-ip 192.0.2.1 \
  --proto tcp \
  --virtual-port 80 \
  --real-ip 10.1.1.1

# 4. Enable another real server (buffered)
yanet-balancer reals enable \
  --name my-balancer \
  --virtual-ip 192.0.2.1 \
  --proto tcp \
  --virtual-port 80 \
  --real-ip 10.1.1.2

# 5. Apply all buffered changes
yanet-balancer reals flush --name my-balancer

# 6. Check state
yanet-balancer state --name my-balancer

# 7. View statistics
yanet-balancer stats \
  --name my-balancer \
  --device eth0 \
  --pipeline main \
  --function balancer \
  --chain default

# 8. View active sessions
yanet-balancer sessions --name my-balancer
```

## Development

### Building

```bash
cargo build --release
```

### Testing

```bash
cargo test
```

### Linting

```bash
cargo clippy
```

### Example Outputs

To see example outputs for all commands without connecting to a gRPC server, run:

```bash
cargo run --example show_outputs
```

This will display sample outputs in all three formats (table, tree, JSON) for:
- config
- list
- state
- stats
- sessions

The example creates mock data structures and uses the actual output formatting functions to demonstrate what the CLI output looks like.

## License

See the main YANET project license.