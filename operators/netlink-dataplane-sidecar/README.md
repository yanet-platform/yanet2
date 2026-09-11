# Netlink dataplane sidecar

The sidecar has three responsibilities in a private Pod network namespace:

- Create explicitly configured VLAN and dummy interfaces as their parents appear.
- Configure KNI, KNI VLANs, kernel `lo` and dummy interfaces once, retrying initial
  setup failures with bounded backoff.
- Read kernel neighbours on KNI/VLAN egress and publish complete atomic snapshots
  to one route-operator table through bounded alternative gateway transports.

The dataplane creates KNI. The sidecar is the only interface configurator in this
namespace. A Netplan or embedded native snapshot is loaded and validated before
runtime resources open. Desired state is immutable until process
restart: editing, replacing or deleting either the sidecar config or the external
Netplan file has no effect on neighbour collection or setup retries.
Invalid or missing input is checked again at the next process startup, not by the
running instance. Host onboard interfaces are not managed.

Creation and configuration run in a bootstrap worker independent of neighbour
collection. It attempts existing links even when another link is missing or its
setup fails. Successful partial setup is retained; idempotent passes retry until
all interfaces are configured, then the worker stops. There is no ongoing drift
repair: deleted or unexpectedly recreated interfaces, changed MTU/addresses,
administrative state or IPv6 settings require a process restart for setup.

Neighbours are collected immediately, on `RTM_NEWNEIGH`, and periodically using
`reconcile.interval` (5m by default). `RTM_DELNEIGH` does not request an immediate
refresh, avoiding withdrawals on transient deletion events; a later full refresh
replaces the whole table. Event wakes are coalesced and use the same common
reconcile loop as the timer, not a separate collection worker or timer. Failures
use reconciliation backoff; an event can wake the loop before that delay ends.
After subscription opens, an extra refresh covers the initial dump/socket gap.
Subscription errors or an unexpectedly closed event channel stop the operator
for supervisor restart. None of this waits for or restarts interface bootstrap.

A kernel deletion operation can also emit `RTM_NEWNEIGH` with `NUD_FAILED`
before `RTM_DELNEIGH`. That state change still triggers collection, matching the
route monitor; ignoring DEL does not suppress other update events.

## Routing and neighbour ownership

The sidecar does not program Linux routes or neighbours, issue neighbour probes,
import Netplan routes/policy/tables, or expose a reverse routing RPC. It configures
specified addresses, MTU, administrative state and per-interface IPv6 settings.
With automatic IPv6LL disabled, it removes only unlisted IPv6LL addresses from
managed non-loopback links after explicit addresses are ensured. It leaves IPv6
and NDP enabled. Existing incompatible objects are reported, not migrated.

Kernel connected/local/RA routes and normal ARP/ND learning are expected kernel
behaviour. Routing remains BIRD export -> bird-adapter FeedRIB -> route-operator
RIB/FIB -> dataplane. Existing route-operator static APIs and BIRD static exports
remain available independently of sidecar routing RPCs.

Explicit MTUs are configured, with parent increases before VLAN increases and
parent decreases after child decreases. An omitted MTU preserves an existing
link's value. A new VLAN inherits its parent's configured MTU, or the observed
parent MTU when unspecified. An oversized existing child with no explicit MTU
blocks a parent decrease; it is not silently resized. Unmanaged dependent links
also block incompatible decreases. MTUs below 1280 are rejected to preserve IPv6.

MTU preflight assumes the sidecar is the sole configurator of this namespace.
The kernel can successfully lower a parent and clamp a VLAN created between
preflight and the write. No other configurator may create or resize links
concurrently. Creation of a VLAN that needs a larger parent MTU is retried after
parent configuration; it does not hold up unrelated links or neighbour polling.

## Configuration

The installed example is `/etc/yanet2/yanet-netlink-dataplane-sidecar-default.yaml`.

| Setting | Contract |
| --- | --- |
| `source` | Required: exactly `netplan` or `native`; missing, empty and unknown values fail startup. |
| `netplan_path` | Only for `source: netplan`; omission uses `/etc/netplan/00-interfaces.yaml`, an explicitly empty path is invalid. |
| `native` | Required mapping for `source: native`; `native: {}` allows an empty topology. Omit `netplan_path`. |
| `link_map` | Managed OS egress name to logical device; omitted entries retain their names. |
| `neighbour_table` | One stable source for this namespace; default `netlink-dataplane-default`. |
| `neighbour_priority` | Positive default source priority; default 100, lower wins per canonical IP. |
| `neighbour_publish_timeout` | Positive per-transport deadline, default 5s. |
| `gateways` | Ordinary connection/TLS settings, alternate paths to the same route operator. |
| `reconcile` | Periodic neighbour refresh interval (5m); event wakes share the same loop. Independent setup and publication retries use the initial/max backoff. |
| `server`, `register` | Common operational metrics endpoint and registration. |

The packaged default explicitly selects `source: netplan`. A Netplan source
rejects a `native` block; a native source rejects a configured `netplan_path` and
never reads an external Netplan file. A missing or null native mapping is invalid.
Both adapters implement `desired.Source.Load() (desired.State, error)` and load
once at startup; the runtime `StateSource` holds a defensive copy, not a loader.
This selector controls desired interfaces and addresses, not neighbour discovery:
neighbours continue to be read from the kernel. It is also independent of the
server-owned neighbour `source` field, which identifies a table.

### Source examples

These are source-specific fragments of the sidecar config. Retain the common
`gateways`, `server`, `register`, `reconcile` and publication settings from the
[packaged default](etc/yanet/yanet-netlink-dataplane-sidecar-default.yaml), adapting
endpoints and `link_map` to the deployment. Choose one source, not both.

For an external Netplan file:

```yaml
source: netplan
netplan_path: /etc/netplan/00-interfaces.yaml
```

The corresponding external file:

```yaml
network:
  version: 2
  ethernets:
    kni0:
      mtu: 1500
      dhcp4: false
      dhcp6: false
      link-local: []
      accept-ra: false
      addresses: [192.0.2.1/24]
    lo:
      addresses: [198.51.100.1/32]
  vlans:
    kni0.100:
      id: 100
      link: kni0
      mtu: 1500
      dhcp4: false
      dhcp6: false
      link-local: []
      accept-ra: false
      addresses: [2001:db8:100::1/64]
  dummy-devices:
    dummy0:
      dhcp4: false
      dhcp6: false
      link-local: []
      accept-ra: false
      addresses: [198.51.100.2/32]
```

Equivalent embedded native configuration, without `netplan_path`:

```yaml
source: native
native:
  ethernets:
    kni0:
      mtu: 1500
      dhcp4: false
      dhcp6: false
      link-local: []
      accept-ra: false
      addresses: [192.0.2.1/24]
    lo:
      addresses: [198.51.100.1/32]
  vlans:
    kni0.100:
      id: 100
      link: kni0
      mtu: 1500
      dhcp4: false
      dhcp6: false
      link-local: []
      accept-ra: false
      addresses: [2001:db8:100::1/64]
  dummy-devices:
    dummy0:
      dhcp4: false
      dhcp6: false
      link-local: []
      accept-ra: false
      addresses: [198.51.100.2/32]
```

### Native subset and DHCP

Native configuration is strict: unknown sections/fields, duplicate keys, null
values and wrong YAML types are rejected, not silently ignored.
Numeric fields require decimal digits with a YAML integer tag; use unquoted
numbers without leading zeros. Forms such as `09000` can receive a YAML float
tag and are rejected. Unlike Netplan, native does not accept quoted integers,
legacy RA booleans such as `no`, or repeated `ipv6` entries in `link-local`.

| Section or field | Supported values |
| --- | --- |
| `ethernets` | Only `kni[0-9]+` and existing kernel `lo`; neither is created. |
| `vlans` | VLAN directly on a declared KNI; required `id` (0..4094) and `link` (parent name). |
| `dummy-devices` | Explicitly declared dummy interfaces; not neighbour egress, like `lo`. |
| `addresses` | List of IP/prefix strings; omitted means empty, not deletion of other addresses. |
| `mtu` | Decimal integer; omitted or 0 means unspecified, otherwise 1280..2147483647 with parent/child validation. |
| `link-local` | Omitted enables IPv6LL; only `[]` or `[ipv6]` is accepted. |
| `accept-ra` | Boolean; omission leaves the kernel setting unchanged. |
| `dhcp4`, `dhcp6` | Optional booleans, only `false`; default disabled. |

All three sections accept the common interface fields above; `id` and `link` are
VLAN-only. There is no `network`/`version`/`renderer` wrapper, `state`/`up` field,
routes, routing-policy, bridges, bonds, tunnels or arbitrary Ethernet support in
native. Netplan keeps its own parser contract, including ignoring routes and
routing-policy; the stricter native subset does not change that behaviour.

For both sources, managed interfaces reject `dhcp4: true` and `dhcp6: true` at
startup, as well as null, string or numeric DHCP values. Omission and boolean
`false` are equivalent. This is configuration validation only: the sidecar does
not start, stop or otherwise control DHCP clients, clear leases, or remove
previously assigned DHCP addresses. Deployment must exclude competing DHCP
clients/configurators. `accept-ra: false` separately controls the IPv6 RA sysctl;
it does not replace `dhcp6: false` or disable IPv6/NDP.

## Neighbour publication and listing

There must be one producer per table. All transports carry the same complete
snapshot; publication stops on the first acknowledged success. An empty complete
snapshot clears this table only. Discovery/preparation/transport failures leave
last-good receiver data intact (except an unacknowledged remote commit).
No table enumeration or prefix cleanup is used.

Discovery uses observed existing managed KNI/VLANs, not their setup status.
Missing configured egress is valid absence, including an empty namespace. Links
without a usable source MAC and neighbours with unusable IP/MAC/NUD state are
omitted; multicast neighbours are ignored. MTU, addresses, administrative state
and DAD completion do not gate collection. An incompatible managed link type,
VLAN parent/tag/protocol, incomplete dump or duplicate unicast IP rejects the
whole snapshot. Identities are checked before and after the neighbour dump;
overlapping link creation, removal, replacement or MAC changes require a retry.
Successful publication proves complete observation, not completed bootstrap.

Discovery also limits the raw namespace dump to 15,252 records **before filtering**.
Unmanaged, multicast and unusable records count towards this limit even though
they are omitted from publication. Exceeding it rejects the entire dump and does
not refresh receiver data. Deployment inventory must account for this scan limit
separately from the output snapshot and named/merged response budgets below.

Each unary `ReplaceNeighbours` carries one full snapshot, limited to 15,252 entries
and 4 MiB (4,194,304 bytes) of protobuf before compression, including framing,
metadata and unknown fields. The entry cap is a technical serialization bound,
not a measured deployment inventory or a capacity guarantee. Table names are
limited to 128 bytes and logical device names to 79 bytes. The receiver validates
the whole request before atomically replacing content and freshness. Oversized
requests fail with `ResourceExhausted`; invalid entries fail with `InvalidArgument`.
There is no truncation or partial publication.

Canonical identity is `IP.Unmap()`, not an IP/device pair. A repeated canonical IP
within a remote snapshot, including the same IP on different devices or an IPv4
and mapped-IPv4 duplicate, rejects the entire snapshot before publication/commit.
Across source tables, the existing priority merge is by IP. Device remains egress
payload for `link_map`/`gateway_devices` mapping and filtering, not scope provenance;
neighbour entries carry no publisher `ifindex`. Loopback and dummy interfaces are
never neighbour egress.

Cancellation observed before commit preserves old content and freshness. A lost
response or client timeout after commit does not roll the table back, so retry
uses the same prepared snapshot. A receiver ACK is success even if the parent
context is subsequently cancelled; it does not acknowledge FIB application.

CLI and Web inspection use unary `List` of one immutable named or merged view.
Each response has a separate 4 MiB protobuf budget including server-owned source,
timestamp and priority metadata. Several valid publications plus static/kernel
entries can exceed the merged budget even when each named view fits. An oversized
view returns `ResourceExhausted`, without a partial list or a streaming fallback.
There is no pagination, chunking or large-table support. Per-message limits do
not bound total process memory: deployment must budget source tables, merged and
old immutable views, and concurrent readers/publishers separately.

## Receiver configuration and readiness

Configure the route operator with local netlink monitoring disabled, explicit
`gateway_devices`, and `readiness.remote_neighbour_table` matching the sidecar.
Set `readiness.remote_neighbour_max_age` greater than the sidecar's periodic
cadence plus transport/retry time (for example 10m for the default 5m cadence).
The receiver cannot derive another process's cadence. Devices outside the
configured gateway topology and duplicate canonical IPs are rejected before commit.

Missing/stale required input prevents new FIB snapshots and preserves last-good
dataplane state. Valid empty and unchanged replacements refresh input readiness;
entry modification timestamps do not measure input freshness. Receiver commit
acknowledgement is independent of subsequent dataplane apply success.

Every new FIB write, including a retry, requires the captured remote generation
to remain current and fresh. Already in-flight RPCs may complete. Incremental
additions, removals and `UpdateTable` priority changes are rejected for the
configured remote table; change its priority through a complete replacement.
Static tables retain their ordinary editing APIs.

Identical next-hop IPs on different devices and scoped neighbour resolution are
not supported. That work and its UI changes are tracked separately in
[issue #2612](https://github.com/yanet-platform/yanet2/issues/2612).
Existing BIRD/FeedRIB/RIB `Route.ifindex` transport and storage remain unchanged;
they do not provide scoped neighbour resolution here.

## Operational endpoint and deployment follow-up

Only the common metrics service is registered by the sidecar. Neighbour collection
metrics use the standard operator collector; setup retries and completion are
logged separately. The Debian package
`yanet2-netlink-dataplane-sidecar` installs `/usr/bin/yanet-netlink-dataplane-sidecar`
and the default config, and depends on CA trust for TLS transports. The
[container recipe](../../deploy/yanet-netlink-dataplane-sidecar.Dockerfile) starts
that binary with `-c /etc/yanet2/yanet-netlink-dataplane-sidecar-default.yaml`.
The external Netplan file is deployment input, not supplied by the sidecar
package. Supply it for `source: netplan`, or replace the sidecar config with
`source: native` and its embedded mapping. Either change takes effect only on
process restart.

Deployment configuration must remove reverse-routing clients, route tuple and
static-interface generation, reverse-RPC probes and permissions. Retain the
operational Service for metrics. Rollout is a separate migration step.

Opt-in kernel integration requires `YANET_NETNS_TESTS=1` and a disposable network
namespace with writable per-interface IPv6 sysctls. A container's read-only
`/proc/sys` mount must be replaced in a disposable mount namespace for these
tests. Missing namespace permissions, kernel support or packaging tools are
unexecuted verification gates, not passing coverage.

The operator integration suite accepts `YANET_ROUTE_OPERATOR_BINARY` pointing to
a freshly built route operator. `Test_Operator_NeighbourPipeline` uses real kernel
neighbours and a recording dataplane gateway for both sources, but supplies
synthetic FeedRIB input. It does not validate a deployed BIRD exporter.
`Test_Operator_ConfigFileRestart` runs both adapters in child processes with
distinct Linux network namespaces, requiring permission to create namespaces in
the test runner. It verifies independent polling and configuration while KNI is
absent, completion after delayed KNI creation, no restoration after completed
setup, continued polling after edits, atomic replacement, invalid YAML or file
deletion, and consumption of new input only by a new process.
