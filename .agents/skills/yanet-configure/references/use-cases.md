# Per-role recipes

Six deployment roles, ranked by real-world frequency (field-notes.md). Each
recipe assumes the canonical bring-up (SKILL.md) finished through step 3:
dataplane + controlplane running, `yanet-cli-inspect` clean.

Common conventions below: controlplane edits go to
`/etc/yanet2/controlplane.d/default.yaml` (then
`systemctl restart yanet2-controlplane@default`); `INSTANCE` is the attach
shorthand `{instance_id: 0, memory_path: *memory_path, memory_requirements: ...}`.

---

## 1. BGP edge / IX router

*route + bird-adapter, driven by the route operator.* Yandex runs YANET at
IX points and DC edges; the classic full-table BGP border may stay on vendor
boxes (habr 716524) - YANET's sweet spot is where line-rate + cheap capacity
matters more than decades of vendor features.

**Controlplane modules**: `route` (16MB), `forward` (16MB) if you also steer
VLANs, devices `plain` (+ `vlan` for tagged handoffs).

**Dataplane**: two PCI ports with workers; wire `connections` both ways
between every pair a route could forward across.

**Route operator** (`/etc/yanet2/yanet-route-operator.yaml`): spine plus the
`function`/`static`/`link_map` blocks from controlplane-and-operators.md.
Every static neighbour needs a valid `device`. BGP-facing kernel traffic
flows over a `virtio_user_<iface>` device (kernel sees `kni0`); map it in
`link_map`.

**BIRD + adapter**: install the patched yanet BIRD, add the `protocol export`
blocks for master4/master6 sockets, point `bird-adapter` at them and at the
route operator. Set `readiness.expect_bird: true`.

**Pipeline**: route-operator publishes `fn:route`; assign it:

```bash
yanet-cli-pipeline update --name main --functions fn:route
yanet-cli-pipeline update --name bypass --functions      # bare flag = empty pipeline
yanet-cli-device-plain update --name 01:00.0 --input main:1 --output main:1
yanet-cli-device-plain update --name 01:00.1 --input main:1 --output main:1
# or declare the same in the pipeline operator (survives restarts)
```

**Verify**:

```bash
yanet-cli-ready route watch                 # rib scope settles after BIRD feed
yanet-cli-operator-route lookup 8.8.8.8 --name route0   # RIB view
yanet-cli-route fib list                    # FIB pushed to dataplane
yanet-cli-counters --module-type route
```

---

## 2. L4 load balancer (anycast VIP, IPIP to real servers)

*l3b + route.* Yandex's largest fleet: ~400 balancers, 12 000 virtual
services, ~1.4 Tbps total, 2x100G per box (habr 967212). The VIP is
announced via BGP (recipe 1 or an external announcer); l3b dispatches flows
to real servers over IPIP tunnels with a session table.

**Controlplane modules**: add `l3b` (64MB) - it is **not** in the shipped
default set - plus `route` for tunnel reachability.

**Virtual service** document (`/etc/yanet2/l3b.d/vs-www.yaml`; schema:
`modules/l3b/cli/src/main.rs`):

```yaml
real_servers:
  - destination_address: 10.0.0.11       # IPIP tunnel endpoint of a real
    source_network: 10.0.0.0/24          # outer source address is derived from it
  - destination_address: 10.0.0.12
    source_network: 10.0.0.0/24
source_filter_rules:                     # which traffic this service accepts
  net6s: ["2001:db8::/32"]
  net4s: ["203.0.113.0/24"]
  port_ranges: [{from: 443, to: 443}]
session_timeouts: {tcp: 3600, tcp_syn: 60, udp: 300, other: 60}
# flags: 1 clamps MSS on SYNs, 2 selects pure L3 balancing
# dscp: {mode: always, value: 46}
```

**Apply + wire**:

```bash
yanet-cli-l3b create-service --name vs-www --file /etc/yanet2/l3b.d/vs-www.yaml
yanet-cli-l3b update-module-config --name l3b0 --file /etc/yanet2/l3b.d/module.yaml
# module.yaml: destination_filter_rules routing VIP traffic to the service:
#   destination_filter_rules:
#     - ... rules naming services by name ...
yanet-cli-function update --name=fn:l3b --chains default:1=l3b:l3b0
yanet-cli-pipeline update --name main --functions fn:route,fn:l3b
```

Operate: `yanet-cli-l3b update-real-server-state --name vs-www --index 0`
(disabled - `enabled` is a set-true flag: omit it to disable, add it to
re-enable), `update-real-server-weight --index N --weight W`,
`list-sessions --name vs-www`.

**Verify**: counters on the VIP rule, `list-sessions` shows flows, real
servers see IPIP-encapsulated traffic with the VIP on their loopback.

---

## 3. Stateful firewall / ACL

*acl + fwstate (+ fwstate-map).* The feature Yandex explicitly picked YANET
for: IPFW-style stateful rulesets at 100G+ with constant lookup depth
(habr 967212), replacing IPVS/iptables boxes that capped at 10M sessions.

**Controlplane modules**: `acl` and `fwstate` - the shipped 8 GB defaults
exist because big rulesets and state tables live in these arenas; size
`cp_memory` accordingly (>= 18 GiB for the full default set).

**Read the canonical fixtures before writing rules**:
`tests/functional/testdata/acl.yaml` (every filter field with worked values)
and `tests/functional/testdata/acl+fwstate.yaml` (the stateful rule pattern).
The ruleset YAML mirrors `modules/acl/controlplane/aclpb/v1/acl.proto`;
unknown keys are rejected, and action kinds must use their declared proto
names (`ACTION_KIND_PASS`, `ACTION_KIND_DENY`, `ACTION_KIND_COUNT`,
`ACTION_KIND_CHECK_STATE`, `ACTION_KIND_CREATE_STATE`, `ACTION_KIND_LOG`) -
the bare names are a hard parse error.

**Rule semantics that shape every ruleset** (from
`modules/acl/dataplane/dataplane.c`):

- First matching rule wins and is terminal. A matched rule whose action list
  ends without a pass (or state hit) **drops the packet**; a packet matching
  no rule is dropped too. Order matters: specific allows first, catch-all
  last.
- Only `ACTION_KIND_COUNT` increments the rule's `counter:` - a deny-only
  rule with a counter name never counts. Put COUNT before the terminal
  action when drops must be visible.
- `proto_ranges` is a 16-bit space: `(ip_proto << 8) | flags-or-icmp-type`.
  Worked values: TCP any flags = 1536..1791, UDP = 4352..4607, any protocol
  = 0..65535. `{from: 6, to: 6}` matches nothing.
- `ACTION_KIND_CHECK_STATE` on a miss falls through to the next action **of
  the same rule**, never to the next rule - pair it with a terminal action
  (the fixture pattern: `CHECK_STATE` then `DENY`).
- `vlan_ranges` is accepted and ignored (deprecated; acl classifies IP
  traffic only).

**Ruleset** (`/etc/yanet2/acl.d/edge.yaml`) - drop bogons, allow inbound
443, established flows pass, the rest denied and counted:

```yaml
rules:
  # bogon sources: counted deny
  - actions: [{kind: ACTION_KIND_COUNT}, {kind: ACTION_KIND_DENY}]
    counter: deny-bogons
    sources4: ["192.0.2.0/24", "198.51.100.0/24"]
  # inbound TCP 443 to the protected host: counted, state created, passed
  # (return traffic of these flows matches no rule here and hits the
  # catch-all below, where the state entry passes it)
  - actions: [{kind: ACTION_KIND_COUNT}, {kind: ACTION_KIND_CREATE_STATE}, {kind: ACTION_KIND_PASS}]
    counter: allow-https
    devices: [{name: 01:00.0}]
    destinations4: ["203.0.113.14/32"]
    proto_ranges: [{from: 1536, to: 1791}]        # TCP, any flags
    dst_port_ranges: [{from: 443, to: 443}]
  # everything else: state hit passes; miss is a counted deny
  - actions: [{kind: ACTION_KIND_CHECK_STATE}, {kind: ACTION_KIND_COUNT}, {kind: ACTION_KIND_DENY}]
    counter: deny-all
```

Other filters: `sources6`/`destinations6`, `src_port_ranges`,
`fragment`. Networks accept CIDR or `/mask` forms.

**Stateful wiring - required, or CHECK_STATE never finds state**
(`tests/functional/main/fwstate_test.go` is the canonical sequence):
create the fwstate-map objects, link both the fwstate and the acl config to
the same maps, and put acl + fwstate in **one function chain**:

```bash
yanet-cli-fwstatemap create --name edge-v4 --kind v4 --index-size 1048576 --extra-bucket-count 1024
yanet-cli-fwstatemap create --name edge-v6 --kind v6 --index-size 1048576 --extra-bucket-count 1024
yanet-cli-fwstate update --name fwstate0 --map-name-v4 edge-v4 --map-name-v6 edge-v6
yanet-cli-acl update --name acl0 /etc/yanet2/acl.d/edge.yaml --map-name-v4 edge-v4 --map-name-v6 edge-v6
yanet-cli-function update --name=fn:acl --chains default:1=acl:acl0,fwstate:fwstate0
yanet-cli-pipeline update --name main --functions fn:acl,fn:route
```

The map links may alternatively live in the ruleset YAML (`fwtable_name_v4`/
`fwtable_name_v6`); an unresolvable map name fails the whole config apply.
`fwstate update` also takes sync/timeouts flags (`--tcp 120s --udp 30s`,
multicast sync peers) for multi-box state sharing.

For DDoS-style scrubbing put `fn:acl` **before** `fn:route` so drops never
hit the FIB. To go declarative, drive the same document through a
generic-operator config (`modules.acl.controlplane.aclpb.v1.ACLService/UpdateConfig`)
- the map objects and the fwstate link still need the CLI sequence above.

**Verify**: `yanet-cli-acl show --name acl0 --counters=nonzero` (per-rule
packet/byte counters), `yanet-cli-acl rule-counters`, `yanet-cli-fwstatemap
list`, `yanet-cli-counters --module-type acl`.

---

## 4. IPv6-only DC border (NAT64 / decap / DSCP)

*nat64 + decap (+ dscp).* Yandex DCs are IPv6-only internally; the edge runs
NAT64 and tunnel termination on YANET boxes (habr 839508).

**Controlplane modules**: `nat64`, `decap`, `dscp` (16MB each).

**Stateless NAT64** (lab scenario `nat64` as canonical example):

```bash
yanet-cli-nat64 prefix add --name nat0 --prefix "64:ff9b::/96"
yanet-cli-nat64 mapping add --name nat0 --ipv4 198.51.100.2 --ipv6 "2001:db8::3" --prefix-index 0
yanet-cli-nat64 mtu --name nat0 --ipv4-mtu 1460 --ipv6-mtu 1420
yanet-cli-function update --name=fn:nat64 --chains default:1=nat64:nat0
```

**IPIP/tunnel termination** (decap - prefixes whose inner traffic is
delivered locally):

```bash
yanet-cli-decap update --name decap0 -p 10.0.0.0/8 -p "2001:db8::/32"
# declarative variant: /etc/yanet2/decap.d/default.yaml with prefixes4/prefixes6
# lists, pushed by yanet2-generic-operator@decap
```

**DSCP marking**: `yanet-cli-dscp prefix-add --name dscp0 -p 203.0.113.0/24`
then `yanet-cli-dscp set-marking --name dscp0 --flag always --mark 46`.

**Pipeline**: `yanet-cli-pipeline update --name main --functions
fn:decap,fn:nat64,fn:route` (decap first - translated packets then route).

**Verify**: per-prefix counters via `yanet-cli-counters`; pcap-level
check with pdump (recipe 6).

---

## 5. L2/L3 traffic steering

*forward.* Port-to-port and VLAN-aware steering with optional L3 filters -
L2 handoffs, VLAN trunks, SDN-style steering.

**Controlplane modules**: `forward` (16MB), devices `plain` + `vlan`.

**Rules** (`/etc/yanet2/forward.d/trunk.yaml`; schema:
`modules/forward/controlplane/forwardpb/v1/forward.proto`):

```yaml
rules:
  - action:
      target: virtio_user_kni0      # redirect matching packets out this device
      mode: OUT
      counter: to-kni0
    devices: [{name: "01:00.0"}]
    vlan_ranges: [{from: 100, to: 200}]
    sources4: ["10.0.0.0/8"]        # all filters optional; widest = catch-all
  - action:
      target: "01:00.0"
      mode: OUT
      counter: to-uplink
    devices: [{name: virtio_user_kni0}]
    vlan_ranges: [{from: 0, to: 4095}]
```

Every cross-device `target` needs a matching dataplane `connections` entry.

**Declarative apply** (shipped pattern,
`yanet2-generic-operator@forward`): the operator config lists
`configs: [{name: trunk, method:
modules.forward.controlplane.forwardpb.v1.ForwardService/UpdateConfig, file:
/etc/yanet2/forward.d/trunk.yaml}]` and publishes `fn:forward` for it.

**Verify**: `yanet-cli-forward show --name trunk`, rule counters
(`counter:` names show up in `yanet-cli-counters`).

---

## 6. Mirroring and capture

*mirror + pdump.* Ops and debugging; zero-risk add-on to any role.

**Controlplane modules**: `mirror`, `pdump` (16MB each).

**Mirror** - copy matching traffic to a monitor port (same rule shape as
forward; mirror targets a device that feeds your collector):

```bash
yanet-cli-mirror update --name mir0 /etc/yanet2/mirror.d/detect.yaml
yanet-cli-function update --name=fn:mirror --chains default:1=mirror:mir0
yanet-cli-pipeline update --name main --functions fn:mirror,fn:route
```

**Pdump** - live tcpdump-style capture in the dataplane (pcap-filter
syntax, 1-64 MiB ring):

```bash
yanet-cli-pdump set --name cap0 --filter "host 203.0.113.14 and port 443"
yanet-cli-pdump read --name cap0
yanet-cli-pdump delete --name cap0
```

Attach pdump to its own function/pipeline stage when it must sit at a
specific point (`fn:pdump` between acl and route is the shipped sample's
`production` stage). `pdump.debug_ebpf: true` in the controlplane config
enables eBPF-assisted debugging.

---

## Module quick reference

| Module | Arena | Config style | One-liner |
|---|---|---|---|
| route | 16MB | FIB YAML / operator RIB | L3 forwarding, ECMP nexthops |
| route-mpls | 16MB | imperative CLI | MPLS label routes |
| forward | 16MB | rules YAML | L2/L3 steering, VLAN trunks |
| decap | 16MB | prefixes4/6 | IPIP tunnel termination |
| nat64 | 16MB | imperative CLI | stateless v4<->v6 translation |
| dscp | 16MB | imperative CLI | DSCP remarking |
| acl | 8GB* | rules YAML | stateful firewall (pair with fwstate; maps linked by name) |
| fwstate | 8GB* | map/sync RPCs | state tables behind acl CHECK_STATE |
| l3b | 64MB | service YAML | L4 load balancing, IPIP, sessions |
| mirror | 16MB | rules YAML | traffic copying |
| pdump | 16MB | pcap filters | in-dataplane capture |
| blackhole | 4MB | counters only | measured drops |
| unrdup | 32MB | named configs | stream replication |

\* shipped defaults; size to the ruleset/state table.
