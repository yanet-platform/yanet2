# Netlink dataplane sidecar

The sidecar has two responsibilities in a private Pod network namespace:

- Restore KNI, KNI VLANs, kernel `lo` and explicitly configured dummy interfaces
  from a Netplan snapshot read and validated once before runtime resources open.
- Read kernel neighbours on KNI/VLAN egress and publish complete atomic snapshots
  to one route-operator table through bounded alternative gateway transports.

The dataplane creates KNI. The sidecar is the only interface configurator in this
namespace and waits for missing KNI. A changed Netplan requires recreating the
whole Pod and its network namespace. Changing or deleting the mounted file does
not reload the running sidecar. Host onboard interfaces are ignored.

## Routing and neighbour ownership

The sidecar does not program Linux routes or neighbours, issue neighbour probes,
import Netplan routes/policy/tables, or expose a reverse routing RPC. It restores
specified addresses, MTU, administrative state and per-interface IPv6 settings.
With automatic IPv6LL disabled, it removes only unlisted IPv6LL addresses from
managed non-loopback links after explicit addresses are ensured. It leaves IPv6
and NDP enabled. Existing incompatible objects are reported, not migrated.

Kernel connected/local/RA routes and normal ARP/ND learning are expected kernel
behaviour. Routing remains BIRD export -> bird-adapter FeedRIB -> route-operator
RIB/FIB -> dataplane. Existing route-operator static APIs and BIRD static exports
remain available independently of sidecar routing RPCs.

Explicit MTUs are restored, with parent increases before VLAN increases and
parent decreases after child decreases. An omitted MTU preserves an existing
link's value. A new VLAN inherits its parent's configured MTU, or the observed
parent MTU when unspecified. An oversized existing child with no explicit MTU
blocks a parent decrease; it is not silently resized. Unmanaged dependent links
also block incompatible decreases. MTUs below 1280 are rejected to preserve IPv6.

MTU preflight assumes the sidecar is the sole configurator of this namespace.
The kernel can successfully lower a parent and clamp a VLAN created between
preflight and the write. Internal serialization does not protect against an
external writer; no other process may create or resize links concurrently.

## Configuration

The installed example is `/etc/yanet2/yanet-netlink-dataplane-sidecar-default.yaml`.

| Setting | Contract |
| --- | --- |
| `netplan_path` | Immutable startup input; default `/etc/netplan/00-interfaces.yaml`. |
| `link_map` | Managed OS egress name to logical device; omitted entries retain their names. |
| `neighbour_table` | One stable source for this namespace; default `netlink-dataplane-default`. |
| `neighbour_priority` | Positive default source priority; default 100, lower wins per IP/device pair. |
| `neighbour_publish_timeout` | Positive per-transport deadline, default 5s. |
| `gateways` | Ordinary connection/TLS settings, alternate paths to the same route operator. |
| `reconcile` | Periodic full restoration/publication and bounded retry backoff. |
| `server`, `register` | Common operational metrics endpoint and registration. |

There must be one producer per table. All transports carry the same complete
snapshot; publication stops on the first acknowledged success. An empty complete
snapshot clears this table only. Preparation/restoration/discovery failure leaves
last-good receiver data intact. No table enumeration or prefix cleanup is used.

The wire limits are 1,000 entries / 256 KiB per chunk, one million entries /
128 MiB per snapshot and four concurrently staged streams per receiver. Canonical
identity is `(IP.Unmap(), logical device)`; observed `ifindex` belongs to the
publisher namespace. Loopback and dummy interfaces are never neighbour egress.

## Receiver configuration and readiness

Configure the route operator with local netlink monitoring disabled, explicit
`gateway_devices`, and `readiness.remote_neighbour_table` matching the sidecar.
Set `readiness.remote_neighbour_max_age` greater than the sidecar's periodic
cadence plus transport/retry time (for example 2m for a 30s cadence). The receiver
cannot derive another process's cadence. Unknown devices or inconsistent scope
are rejected before commit.

Missing/stale required input prevents new FIB snapshots and preserves last-good
dataplane state. Valid empty and unchanged replacements refresh input readiness;
entry modification timestamps do not measure stream freshness. Receiver commit
acknowledgement is independent of subsequent dataplane apply success.

Every new FIB write, including a retry, requires the captured remote generation
to remain current and fresh. Already in-flight RPCs may complete. Incremental
additions and removals are rejected for the configured remote table; static
tables retain their ordinary editing APIs.

Scoped BIRD routes require BIRD and sidecar to share a namespace. The configured
source binds observed ifindex to device before static neighbour priority is
applied. Missing scope never falls back to another device; ambiguous unscoped
next hops are unresolved. Deployment of scoped forwarding requires validating
the actual patched BIRD exporter attribute `0x1200` against Linux and FeedRIB
indices; synthetic decoder tests alone do not establish that provenance.

## Operational endpoint and deployment follow-up

Only the common metrics service is registered by the sidecar. Interface/neighbour
reconciliation metrics use the standard operator collector. The Debian package
installs the binary, default config and CA trust dependency for TLS transports.

Deployment configuration must remove reverse-routing clients, route tuple and
static-interface generation, reverse-RPC probes and permissions. Retain the
operational Service for metrics. Rollout is a separate migration step.

Opt-in kernel integration requires `YANET_NETNS_TESTS=1` and a disposable network
namespace. Missing userns permissions, deployed BIRD exporter or packaging tools
are unexecuted verification gates, not passing coverage.

The operator integration suite accepts `YANET_ROUTE_OPERATOR_BINARY` pointing to
a freshly built route operator. `Test_Operator_NeighbourPipeline` uses real kernel
neighbours and a recording dataplane gateway, but supplies synthetic FeedRIB input.
`Test_Operator_NetplanNamespaceRestart` launches two child processes with distinct
Linux network namespaces, requiring permission to create namespaces in the test
runner. It verifies address restoration from the original snapshot after file
replacement and consumption of the replacement only by the second instance.
