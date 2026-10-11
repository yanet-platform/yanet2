---
name: yanet-configure
description: Configure, size and bring up a YANET (yanet2) DPDK software router for its common deployment roles - BGP edge router, L4 load balancer, stateful firewall/ACL, NAT64/decap border, L2/L3 traffic steering, mirroring and capture. Use whenever the user asks to set up, deploy, size or reconfigure a YANET box, write /etc/yanet2 dataplane or controlplane or operator configs, enable or tune modules (route, acl, fwstate, l3b, nat64, decap, forward, mirror, pdump, dscp), wire functions/pipelines/devices, or verify a running deployment - even if they only say "make this box route" or "add NAT64 here".
---

# Configuring YANET

Bring a YANET box from bare metal to a working deployment role. Configs here
mirror the packaged samples in the tree (`dataplane.yaml`,
`controlplane/etc/yanet/controlplane.d/default.yaml`,
`operators/*/etc/yanet/*-default.yaml`). If an example and the tree disagree,
the tree wins - re-read the shipped sample before writing config.

## The configuration model (read this first)

Four layers configure a box. Know which layer owns the object you are changing,
or an operator will silently revert your edit on its next reconcile pass.

| Layer | Unit / process | Owns | Change cadence |
|---|---|---|---|
| Dataplane | `yanet2-dataplane.service` | `/etc/yanet2/dataplane.yaml`: ports, workers, core pinning, shared-memory size | restart to change |
| Controlplane | `yanet2-controlplane@<i>.service` | `/etc/yanet2/controlplane.d/<i>.yaml`: which modules and device adapters start, their shared-memory attach (`instance_id`, `memory_requirements`) | restart to change |
| Operators | `yanet2-route-operator`, `yanet2-generic-operator@<name>`, `yanet2-pipeline-operator`, `yanet2-bird-adapter` | declarative state, reconciled every ~30 s: FIB, module configs, functions, pipelines, device assignment, BGP import | live |
| CLI | `yanet-cli-*` | imperative one-shot changes against the gateway | immediate |

Invariant: the dataplane keeps processing with the **last valid config** if an
upper layer fails or pushes garbage. A broken operator push does not stop
already-forwarded traffic - but it also means "the CLI accepted it" is not
"the dataplane applied it". Always verify (commands below).

### Object wiring

Runtime packet processing is assembled from named objects:

```
module config (decap0, acl0, route0, ...)     -- per-module rules/state, created
        |                                        via module CLI or generic operator
        v
function fn:<name> (chains: type:name, ...)   -- module configs bound into chains
        |
        v
pipeline (ordered function list)              -- e.g. fn:decap, fn:acl, fn:route
        |
        v
device (input/output pipelines with weights)  -- physical/virtio ports
```

- A module config not referenced by any function does nothing.
- A device with no input pipeline assigned drops its traffic; assign at least
  an empty pipeline for pass-through (the shipped samples' `bypass`, the lab's
  `dummy`).
- Modules that redirect packets across devices (e.g. forward rules with
  `action.target`) need the devices wired in dataplane.yaml `connections:` -
  those entries create the worker-to-worker pipes cross-device output uses.

## Pick the deployment role

Ranked by how often they appear in real deployments. Field evidence and
sources: `references/field-notes.md` (Yandex - the only public production
operator - runs exactly these families).

| Role | Modules | Driven by | Recipe |
|---|---|---|---|
| BGP edge / IX router | route, bird-adapter | route-operator + BIRD | `references/use-cases.md` #1 |
| L4 load balancer (anycast VIP, IPIP) | l3b (+ route for VIP/reals) | CLI / operators | #2 |
| Stateful firewall / ACL | acl + fwstate (+ fwstate-map) | CLI or generic-operator | #3 |
| IPv6-only DC border translation | nat64 + decap (+ dscp) | CLI or generic-operator | #4 |
| L2/L3 traffic steering | forward | generic-operator | #5 |
| Mirroring & capture | mirror + pdump | CLI | #6 |

Trying an untested change? Validate it in the QEMU lab first - see the
`yanet-lab` skill; same config surface, zero production risk.

## Canonical bring-up

```bash
# 0. Host prep (details and sizing: references/host-and-dataplane.md)
sudo sysctl -w vm.nr_hugepages=2048                 # 4 GiB; persist in sysctl.d
sudo modprobe vfio-pci
sudo subprojects/dpdk/usertools/dpdk-devbind.py --status
sudo subprojects/dpdk/usertools/dpdk-devbind.py --bind=vfio-pci 01:00.0

# 1. Dataplane: copy the shipped sample and edit (ports, cores, memory math!)
sudo cp /etc/yanet2/dataplane-default.yaml /etc/yanet2/dataplane.yaml
sudo systemctl start yanet2-dataplane

# 2. Controlplane: edit /etc/yanet2/controlplane.d/default.yaml
#    (trim the modules map to the role; add l3b - it is not in the default set)
sudo systemctl start yanet2-controlplane@default

# 3. Sanity check before operators
yanet-cli-inspect            # dataplane up, modules attached
yanet-cli-gateway list       # built-in backends registered

# 4. Operators + module configs + wiring: per-role recipe in references/use-cases.md

# 5. Verify
yanet-cli-ready --all
yanet-cli-counters            # counter table; filter with --module-type/--function/…
```

CLI endpoint defaults to `grpc://[::1]:8080`; override per call with
`--endpoint`, via `YANET_ENDPOINT`, or `/etc/yanet2/cli.yaml`.

## Sizing rules of thumb

- Shared-memory file (`storage:`, default `/dev/hugepages/yanet`) is sized at
  dataplane start to the sum of every instance's `dp_memory + cp_memory`.
- `cp_memory` must cover the **sum of all enabled modules' and devices'
  `memory_requirements`** (agent arenas carve out of it). The shipped
  controlplane default enables acl 8 GB + fwstate 8 GB + ~150 MB others, while
  the shipped dataplane sample has `cp_memory: 2 GiB` - either trim the module
  map to the role or raise `cp_memory` (>= 18 GiB for the full default set).
- Hugepages total must cover that file plus `dpdk_memory` and mbuf pools.
- One busy-poll core per worker; leave cores for the OS and the control plane.
  Yandex also disables hyperthreading and turbo boost on production boxes
  (field-notes).
- NICs: Mellanox ConnectX-4/5/6 are the production-tested family.

## Verification commands

```bash
yanet-cli-inspect                              # dataplane + module attach state
yanet-cli config show                          # CLI connection settings + sources
yanet-cli-gateway list                         # which operators are registered
yanet-cli-ready --all watch                    # readiness scopes go READY
yanet-cli-pipeline list && yanet-cli-function list
yanet-cli-counters [--module-type ...] [--function ...] # counter table
yanet-cli-pdump set --name cap0 --filter "host 203.0.113.14"
yanet-cli-pdump read --name cap0                # capture what actually forwards
```

## Pitfalls

- Both config loaders are strict: an unknown YAML key is a hard error, not a
  warning. `instance_id` must be written out even when it is 0 - in the
  gateway block and in every module/device entry.
- CLI-created runtime objects (module configs, functions, fwstate maps) live
  in shared memory: the dataplane keeps forwarding on them, but a
  controlplane restart loses them from the management view. Anything that
  must survive restarts belongs in a generic-operator config file or the
  pipeline operator's stages.
- Packet-filter details have sharp edges - ACL action kinds are declared
  proto names (`ACTION_KIND_DENY`, not `DENY`), `proto_ranges` is the 16-bit
  `(proto << 8) | flags` space. Copy values from the canonical fixtures in
  `tests/functional/testdata/*.yaml` instead of hand-deriving them.
- Operator vs CLI ownership: an operator re-applies its objects every
  reconcile pass. Hand-edits to `fn:route`, operator-managed module configs or
  the pipeline assignment get reverted within ~30 s. The supported pattern for
  manual/experimental modules: declare a hook function (lab convention:
  `fn:lab`) in the pipeline-operator's function list, but create and own that
  function yourself with `yanet-cli-function update`.
- Route-operator validates static neighbour entries: each needs a `device`
  matching a dataplane device name; `link_map` maps logical link names to OS
  interfaces for the netlink monitor.
- `iova_mode: pa` is required for uio/vfio-no-iommu (typical in VMs); `va` for
  `virtio_user_*` ports.
- `virtio_user_<iface>` devices are the kernel-facing side (management traffic,
  BGP/BIRD sessions); on PCI ports YANET owns the link and the kernel never
  sees it.
- Deleting a module config referenced by a pipeline fails by design - detach
  the function first.
- After a shared-memory layout change (module set, `memory_requirements`), a
  plain restart is not enough on a dirty host: remove stale
  `/dev/hugepages/yanet*` files if the dataplane refuses to start.

## References (read on demand)

- `references/host-and-dataplane.md` - host prep, dataplane.yaml grammar,
  port types, memory math.
- `references/controlplane-and-operators.md` - controlplane YAML, all four
  operators, CLI wiring syntax, the `fn:lab` hook pattern.
- `references/use-cases.md` - the six per-role recipes with full config
  examples and verify steps.
- `references/field-notes.md` - what real deployments look like (Yandex
  production stories, numbers, hardware, sources).
