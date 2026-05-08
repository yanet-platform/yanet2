# BIRD Adapter

Adapter for importing routes from BIRD into YANET.

## Architecture

```
BIRD daemon → Unix Socket → bird-adapter server → gRPC → route service → RIB → FIB
                                ↑
                          bird-adapter client (configuration)
```

## Components

### Server ([`server.go`](server.go:1))
gRPC server managing BIRD route imports:
- Listens on `listen_addr` for configuration requests
- Connects to `gateway_endpoint` (route service) to send route updates
- Manages multiple imports simultaneously

**Note:** `gateway_endpoint` is the address where the controlplane gateway listens. The gateway knows how to forward requests to the route module agent.

### Client ([`client.go`](client.go:1))
CLI for configuring the server:
- Sends import configuration to the server
- Specifies paths to BIRD Unix sockets

### Adapter Service ([`modules/route/bird-adapter/service.go`](../../modules/route/bird-adapter/service.go:1))
Import logic:
- Reads routes from BIRD via [`bird.Export`](../../modules/route/internal/discovery/bird/export.go:1)
- Streams updates to route service via [`FeedRIB`](../../modules/route/controlplane/service.go:305)
- Automatically reconnects on errors
- Manages sessions for stale route cleanup

## Usage - Quick Start

### Start Server

```bash
yanet-bird-adapter server -c config.yaml
```

**config.yaml:**
```yaml
logging:
  level: info
listen_addr: "localhost:50051"
gateway_endpoint: "localhost:8080"
```

### Configure Import

```bash
yanet-bird-adapter client \
  --server-config config.yaml \
  --config route0 \
  --sockets /var/run/bird/bird.sock,/var/run/bird/bird6.sock
```

**Parameters:**
- `--server-config` — path to server config (to get `listen_addr`)
- `--config` — route configuration name
- `--sockets` — comma-separated list of BIRD Unix socket paths

## BIRD Protocol

Parses BIRD binary export format:
- Prefixes: IPv4/IPv6/VPN4/VPN6
- Operations: insert/remove
- BGP attributes: AS_PATH, NEXT_HOP, MED, LOCAL_PREF, Large Communities

## Route Management

After import, use [`route` CLI](../../modules/route/cli/route/src/main.rs:1):

```bash
# Show imported routes
route show -c route0

# Lookup route
route lookup 10.0.0.1 -c route0

# Sync RIB → FIB
route flush -c route0
```

## Fault Tolerance

- **Automatic reconnection** on connection loss to BIRD or route service
- **Exponential backoff** for retry attempts
- **Session management** for stale route cleanup on restart
- **Graceful shutdown** on termination signal