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

### module_config

- `virtual_services` - List of virtual services
  - `ip` - Virtual IP address
  - `port` - Port number
  - `proto` - Protocol (tcp/udp)
  - `scheduler` - Scheduler algorithm (wrr/prr/wlc)
  - `flags` - Service flags (gre, fix_mss, ops, pure_l3)
  - `allowed_srcs` - List of allowed source networks (CIDR)
  - `reals` - List of real servers
    - `weight` - Server weight
    - `dst` - Destination IP
    - `src` - Source IP for forwarding
    - `src_mask` - Source mask
    - `enabled` - Enable/disable flag
  - `peers` - List of peer balancer IPs

- `source_address_v4` - IPv4 source address
- `source_address_v6` - IPv6 source address
- `decap_addresses` - List of decapsulation addresses
- `sessions_timeouts` - Session timeout configuration
- `wlc` - WLC scheduler configuration

### module_state_config

- `session_table_capacity` - Maximum sessions
- `session_table_scan_period_ms` - Scan period in milliseconds
- `session_table_max_load_factor` - Max load factor (0.0-1.0)

See [`example-config.yaml`](example-config.yaml) for a complete example.

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