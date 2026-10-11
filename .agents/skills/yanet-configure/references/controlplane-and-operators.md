# Controlplane and operators

How `/etc/yanet2/controlplane.d/<i>.yaml` starts modules, and how each
operator drives runtime state. Shipped samples:
`controlplane/etc/yanet/controlplane.d/default.yaml` (fully commented - read
it), `operators/*/etc/yanet/*-default.yaml`.

## Controlplane YAML

```yaml
logging:
  level: info
memory_path: &memory_path /dev/hugepages/yanet
gateway:
  instance_id: 0                     # required, even when 0
  server:
    endpoint: "[::1]:8080"           # gRPC for CLIs and operators
    http_endpoint: "[::1]:8081"      # HTTP proxy of gRPC (web UI)
    # tls: {cert_file: ..., key_file: ...}
  auth:
    disabled: true                   # see identities/permissions samples in
                                     # controlplane/etc/yanet/auth/
  registry:                          # operator registration TTL/eviction
    preserve_stale_backends: false
    ttl: 5m
    sweep_interval: 30s
modules:                             # a module absent here is NOT started
  route:      {instance_id: 0, memory_path: *memory_path, memory_requirements: 16MB}
  # ... 16MB for decap/dscp/forward/mirror/nat64/pdump/route-mpls,
  #      64MB for acl and l3b-scale zones, 8GB for acl+fwstate defaults,
  #      4MB blackhole, 32MB unrdup; l3b is NOT in the shipped default set
devices:                             # device adapters, same attach shape
  plain:      {instance_id: 0, memory_path: *memory_path, memory_requirements: 16MB}
  vlan:       {instance_id: 0, memory_path: *memory_path, memory_requirements: 16MB}
```

Per-module controlplane extras: `route.disable_nexthop_counters`,
`pdump.debug_ebpf`, `fwstate.stale_layer_sweep_interval`; most modules also
accept `endpoint` (own gRPC listen address, default `[::1]:0` = free port).

`memory_path` defaults to `/dev/hugepages/yanet`; `instance_id` is mandatory
in every entry.

Unit: `yanet2-controlplane@default` (template; config file must exist:
`/etc/yanet2/controlplane.d/default.yaml`). Restart to apply - module set and
arena sizes are attach-time decisions.

## Operators

All operators dial **out** to gateway endpoints, self-register (heartbeat
`register.interval`, default 30 s; evicted after `registry.ttl`), and
reconcile desired state every `reconcile.interval`. All share this config
spine:

```yaml
logging: {level: info}
gateways:
  - name: numa0                      # one per dataplane instance/NUMA node
    endpoint: "[::1]:8080"
register:  {interval: 30s}
reconcile: {interval: 30s, initial_backoff: 500ms, max_backoff: 30s}
```

### Route operator (`yanet2-route-operator`)

Owns the RIB -> FIB path and publishes exactly one function
(`fn:route` -> module `route0`). Key fields beyond the spine
(`operators/route/etc/yanet/yanet-route-operator-default.yaml`):

```yaml
function:
  name: "fn:route"
  chain: "default"
  weight: 1
  module: "route0"
static:                              # seed data applied at startup
  routes:
    - {prefix: 0.0.0.0/0, nexthop_addr: 203.0.113.1}
    - {prefix: "::/0", nexthop_addr: "fe80::1"}
  neighbours:                        # device must match a dataplane device
    - next_hop: 203.0.113.1         # name; validated on load
      link_addr: "52:54:00:6b:ff:a1"     # gateway MAC
      hardware_addr: "52:54:00:6b:ff:a5" # own port MAC
      device: "01:00.0"
link_map: {kni0: "01:00.0"}          # logical link -> OS iface for netlink
netlink_monitor: {disabled: false, table_name: kernel, default_priority: 100}
readiness:
  expect_bird: false                 # true when a BIRD adapter feeds the RIB
  rate_threshold: 50
  stability_window: 5s
  sample_interval: 1s
  reconnect_grace: 15s
```

`gateway_devices` optionally restricts which dataplane devices a gateway's
FIB nexthops use. The FIB itself is rebuilt from RIB + neighbours each
reconcile - do not hand-edit it via CLI on an operator-managed box.

### Generic operator (`yanet2-generic-operator@<name>`)

Pushes any module's `UpdateConfig` document and publishes functions for it.
Template unit: config in `/etc/yanet2/generic-operator.d/<name>.yaml`.
Shipped examples: `forward-default.yaml`, `decap-default.yaml`
(`operators/generic/etc/yanet/`):

```yaml
name: decap
logging: {level: info}
gateways: [{name: numa0, endpoint: "[::1]:8080"}]
register:  {interval: 30s}
reconcile: {interval: 30s, initial_backoff: 500ms, max_backoff: 30s}
configs:
  - name: decap0
    method: modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig
    file: /etc/yanet2/decap.d/default.yaml
functions:
  - name: fn:decap
    chains:
      - chain: {name: default, modules: [{type: decap, name: decap0}]}
        weight: 1
```

The `method` is the fully-qualified gRPC method of the module's control-plane
service (`modules.<m>.controlplane.<m>pb.v1.<M>Service/UpdateConfig`); the
`file` is the module's own YAML document (see use-cases). This is the
declarative alternative to `yanet-cli-<module> update` for anything that must
survive restarts.

### Pipeline operator (`yanet2-pipeline-operator`)

Assembles functions into pipelines and binds pipelines to devices:

```yaml
stages:
  - name: production
    pipelines:
      - name: main
        functions: ["fn:decap", "fn:acl", "fn:route"]
      - name: bypass
        functions: []                # empty = pass-through
    devices:
      plain:
        - name: virtio_user_kni0    # dataplane device name
          input:  [{name: main, weight: 1}]
          output: [{name: bypass, weight: 1}]
      vlan: []
```

Declare manually-owned hook functions (e.g. `fn:lab`) in the pipeline's
function list here, but create/update the function itself with
`yanet-cli-function` - the operator reconciles the list membership, not the
function contents. This is the supported way to mix managed and manual
modules in one pipeline.

### BIRD adapter (`yanet2-bird-adapter`)

Feeds BIRD tables into the route operator's RIB (BGP/static/anything BIRD
speaks). `operators/bird-adapter/etc/yanet/bird-adapter-default.yaml`:

```yaml
logging: {level: info}
listen_addr: "localhost:50051"
route_operator_endpoint: "[::1]:8080"   # operator's own server or the gateway
bird:
  name: route0
  sockets:
    - /var/run/bird/yanet-master4.sock
    - /var/run/bird/yanet-master6.sock
  source_v4: "127.0.0.1"
  source_v6: "::1"
```

BIRD side needs the yanet export protocol (patched BIRD 2.15 `yanet-...`
build; pinned in `lab/operators.go`):

```text
router id 127.0.0.1;
protocol device {}
protocol static static4 { ipv4; route 198.51.100.0/24 via 203.0.113.1; }
protocol export lab4 { table master4; socket "/var/run/bird/yanet-master4.sock"; }
protocol export lab6 { table master6; socket "/var/run/bird/yanet-master6.sock"; }
```

Set `readiness.expect_bird: true` on the route operator when the adapter is
in front, so the RIB scope stays SYNCING until the feed settles.

### Neighbour sidecar (`yanet-neighbour-sidecar`)

Publishes kernel neighbour entries to gateways (`table_name`,
`default_priority`, `update_interval`); used when neighbour discovery must be
shared/supervised outside the route operator.

## CLI wiring syntax

Gateway endpoint for all CLIs: `--endpoint grpc://[::1]:8080` (env
`YANET_ENDPOINT`, `/etc/yanet2/cli.yaml`).

```bash
# function: chains are chain:weight=type:name[,type:name...] - a comma list
# puts several modules in ONE chain (acl + fwstate must share a chain)
yanet-cli-function update --name=fn:lab --chains default:1=decap:decap_lab
yanet-cli-function update --name=fn:acl --chains default:1=acl:acl0,fwstate:fwstate0

# pipeline: ordered function list; a bare --functions makes an empty
# (pass-through) pipeline
yanet-cli-pipeline update --name main --functions fn:decap,fn:route
yanet-cli-pipeline update --name bypass --functions

# device: bind pipelines with weights, input and output - per-adapter binary
yanet-cli-device-plain update --name kni0 --input main:1 --output bypass:1
```

The dispatcher `yanet-cli` forwards to per-module binaries:
`yanet-cli device plain <cmd>` -> `yanet-cli-device-plain`,
`yanet-cli operator <child>` -> `yanet-cli-operator-*`. There is no bare
`yanet-cli-device` binary. Useful core CLIs: `yanet-cli-inspect [--memory]`,
`yanet-cli-counters`, `yanet-cli-gateway list`, `yanet-cli-ready [service]
[watch]`, `yanet-cli config show`, `yanet-cli-ready --all`.
