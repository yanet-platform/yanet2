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

#### 1. update-config

Update balancer configuration from a YAML file.

```bash
yanet-balancer update-config \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  --config-file <PATH_TO_YAML>
```

**Example:**
```bash
yanet-balancer update-config \
  --name my-balancer \
  --instance 0 \
  --config-file example-config.yaml
```

See [`example-config.yaml`](example-config.yaml) for configuration file format.

#### 2. reals update

Update a real server (always buffered).

```bash
yanet-balancer reals update \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  --virtual-ip <VIP> \
  --proto <tcp|udp> \
  --virtual-port <PORT> \
  --real-ip <REAL_IP> \
  [--enable | --disable] \
  [--weight <WEIGHT>]
```

**Examples:**
```bash
# Enable a real server
yanet-balancer reals update \
  --name my-balancer \
  --virtual-ip 192.0.2.1 \
  --proto tcp \
  --virtual-port 80 \
  --real-ip 10.1.1.1 \
  --enable

# Disable a real server
yanet-balancer reals update \
  --name my-balancer \
  --virtual-ip 192.0.2.1 \
  --proto tcp \
  --virtual-port 80 \
  --real-ip 10.1.1.2 \
  --disable

# Update weight
yanet-balancer reals update \
  --name my-balancer \
  --virtual-ip 192.0.2.1 \
  --proto tcp \
  --virtual-port 80 \
  --real-ip 10.1.1.1 \
  --enable \
  --weight 200
```

#### 3. reals flush

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

#### 4. show-config

Show balancer configuration.

```bash
yanet-balancer show-config \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  [--format <table|json|tree>]
```

**Examples:**
```bash
# Show as table (default)
yanet-balancer show-config --name my-balancer

# Show as JSON
yanet-balancer show-config --name my-balancer --format json

# Show as tree
yanet-balancer show-config --name my-balancer --format tree
```

#### 5. list-configs

List all balancer configurations.

```bash
yanet-balancer list-configs [--format <table|json|tree>]
```

**Example:**
```bash
yanet-balancer list-configs --format table
```

#### 6. config-stats

Show configuration statistics.

```bash
yanet-balancer config-stats \
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
yanet-balancer config-stats \
  --name my-balancer \
  --instance 0 \
  --device eth0 \
  --pipeline main \
  --function balancer \
  --chain default \
  --format table
```

#### 7. state-info

Show balancer state information (active sessions, VS info, real info).

```bash
yanet-balancer state-info \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  [--format <table|json|tree>]
```

**Example:**
```bash
yanet-balancer state-info --name my-balancer --format table
```

#### 8. sessions-info

Show active sessions information.

```bash
yanet-balancer sessions-info \
  --name <CONFIG_NAME> \
  --instance <INSTANCE> \
  [--format <table|json|tree>]
```

**Example:**
```bash
yanet-balancer sessions-info --name my-balancer --format table
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
yanet-balancer update-config \
  --name my-balancer \
  --config-file config.yaml

# 2. Check configuration
yanet-balancer show-config --name my-balancer

# 3. Disable a real server (buffered)
yanet-balancer reals update \
  --name my-balancer \
  --virtual-ip 192.0.2.1 \
  --proto tcp \
  --virtual-port 80 \
  --real-ip 10.1.1.1 \
  --disable

# 4. Enable another real server (buffered)
yanet-balancer reals update \
  --name my-balancer \
  --virtual-ip 192.0.2.1 \
  --proto tcp \
  --virtual-port 80 \
  --real-ip 10.1.1.2 \
  --enable

# 5. Apply all buffered changes
yanet-balancer reals flush --name my-balancer

# 6. Check state
yanet-balancer state-info --name my-balancer

# 7. View statistics
yanet-balancer config-stats \
  --name my-balancer \
  --device eth0 \
  --pipeline main \
  --function balancer \
  --chain default

# 8. View active sessions
yanet-balancer sessions-info --name my-balancer
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
- show-config
- list-configs
- state-info
- config-stats
- sessions-info

The example creates mock data structures and uses the actual output formatting functions to demonstrate what the CLI output looks like.

## License

See the main YANET project license.