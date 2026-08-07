# YANET2 Lab

The lab turns the functional-test QEMU harness into a reusable local
YANET2 environment. A supervisor keeps one VM alive across commands, while
snapshot reset makes experiments repeatable.

## Quick start

```bash
just lab doctor
just lab up
just lab status
just lab scenario list
just lab scenario run forward-route
just lab report
just lab down
```

`just lab` without a subcommand prints quick-start help and does not start a
VM. Build host artifacts first with `make all`; the lab also needs the
functional-test image and operator binaries produced by the Meson build.

The first `up` can take several minutes because the functional harness may need
to prepare its base image, baseline snapshot, and pinned
`yanet-bird2 2.15.1.1785924912.af804ec4-1` package. Later starts and `reset`
reuse the snapshot. Set `YANET_QEMU_IMAGE` to use a non-default image. If an
existing session is unhealthy, `up` returns its status instead of replacing its
supervisor; inspect it with `status`, `report`, or `down`.

## Commands

- `doctor` checks Go, Just, QEMU, the disk image, required host artifacts, and
  optional Linux KVM.
- `up` starts or reuses a named session; `--session NAME` selects another one.
- `status` verifies the dataplane, control plane, operators, BIRD session, and
  imported lab routes.
- `exec -- COMMAND ARG...` executes one command without shell interpolation.
- `shell` opens an SSH-backed interactive Bash session in the guest.
- `serial` attaches directly to the guest's ttyS0 Bash console. Press `Ctrl-]`
  to detach without stopping the VM.
- `reset` restores the known-good operator baseline QEMU snapshot.
- `report` records process and `kni0` state in the session directory.
- `down` stops the VM and supervisor.
- `scenario list|run` discovers and runs the built-in guided scenarios.
- `manifest validate|run` checks or executes a custom manifest.

Add `--json` to commands intended for scripts. Exit status is non-zero when an
environment check, manifest validation, command, or packet probe fails.

## Manifests

Manifests are YAML documents governed by
[`lab/yanet2-lab.schema.json`](../lab/yanet2-lab.schema.json). Editors can load
the schema through the manifest's `$schema` field. Validation is deliberately
strict: unknown properties are rejected at every level, command arguments are
arrays, names must be unique, referenced files must exist, and paths cannot
escape the manifest directory.

```yaml
$schema: ../../yanet2-lab.schema.json
version: 1
name: example
steps:
  - name: inspect
    argv: [/tmp/yanet/cli/yanet-cli-inspect]
    timeout: 10s
probes:
  - name: packet
    ingress: 0
    egress: 0
    timeout: 1s
    send: {pcap: input.pcap}
    expect: {pcap: expected.pcap}
```

The first version intentionally exposes only the two QEMU packet ports used by
the functional framework, numbered `0` and `1`. Each probe currently accepts
one packet in its input PCAP and either one expected packet (compared after
stripping Ethernet padding) or `expect: {drop: true}`. On mismatch, the
report includes both packets as hex.

`files` can copy local fixtures into the guest before steps run. Its
`destination` is a guest path; `source` is relative to the manifest. Commands
are transported as `argv`, quoted by the runner, and never evaluated by the
host shell. Fixture files are limited to 64 KiB and packet captures must use
the Ethernet link type.

`boot` starts YANET with custom dataplane and controlplane YAML instead of the
baseline. It restores the pre-YANET snapshot, so the operator baseline
(route/forward/decap/pipeline, BIRD, `fn:lab`) is not available in boot
manifests. Common baseline configuration (kni0 interface, forwarding rules,
route FIB) is still applied after the custom boot.

## Built-in scenarios

- `forward-route` validates the baseline forward/route pipeline with a UDP
  packet and an exact PCAP expectation.
- `decap` configures an unmanaged decapsulation module and verifies an exact
  IPv4-in-IPv4 transformation.
- `nat64` configures an unmanaged prefix and mapping and verifies an exact
  IPv4-to-IPv6 transformation.

Run `reset` between unrelated experiments. Scenario execution is fail-fast and
writes its latest structured report to the session runtime directory under the
system temporary directory.

## Operator baseline

The saved lab baseline runs the route, forward, decap, and pipeline operators,
BIRD, and the BIRD adapter. It includes permanent IPv4 and IPv6 neighbours,
default routes, and BIRD routes for `198.51.100.0/24` and
`2001:db8:100::/48`. `status` requires every operator readiness service, the
active `route0` adapter session, and both BIRD routes.

BIRD comes from the pinned
[`v2.15.1-yanet.1`](https://github.com/yanet-platform/bird/releases/tag/v2.15.1-yanet.1)
release asset. It is built from `af804ec4`, which exports the interface index
the current BIRD adapter requires.

The operators reconcile `fn:route`, `fn:forward`, `fn:decap`, and the `test`
pipeline. Use `fn:lab` for manual modules that should participate in that
pipeline without being overwritten by reconciliation. The built-in decap and
NAT64 scenarios follow this rule and use separate `decap_lab` and `nat64_lab`
module names.

## Guest shells

Both `just lab shell` and `just lab serial` start Bash with
`/tmp/yanet/cli` on `PATH`. They enable each YANET CLI's dynamic Bash
completion and show the guest paths for CLI binaries, configuration, logs, and
build artifacts. `shell` is the normal choice because it has a native SSH TTY;
use `serial` to inspect the QEMU console directly. Other VM commands report
`lab is busy` while serial is attached; `down` closes the attachment. `reset`
restores the guest snapshot and reapplies this shell setup.

## Troubleshooting

Run `just lab doctor` first. If `up` fails, its error points to
`supervisor.log`, which contains the functional harness and QEMU startup logs.
Use `just lab report`, `just lab status`, and guest commands such as
`just lab exec -- ps aux` before resetting the VM. Every runtime directory is
owned by the current user with mode `0700`; keys, sockets, locks, and reports
reject unsafe file types or permissions. Reports do not copy the host
environment or other secret sources.

Development ideas and delivered milestones live in
[`lab/ROADMAP.md`](../lab/ROADMAP.md).
