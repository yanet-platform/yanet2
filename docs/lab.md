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

The first `up` can take several minutes because the functional harness may need
to prepare its base image and baseline snapshot. Later starts and `reset` reuse
the snapshot. Set `YANET_QEMU_IMAGE` to use a non-default image.

## Commands

- `doctor` checks Go, Just, QEMU, the disk image, and optional Linux KVM.
- `up` starts or reuses a named session; `--session NAME` selects another one.
- `status` verifies that the supervisor and YANET dataplane are alive.
- `exec -- COMMAND ARG...` executes one command without shell interpolation.
- `shell` opens a small interactive command loop over the guest serial console.
- `reset` restores the known-good `baseline` QEMU snapshot.
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
one packet in its input PCAP and either one exact expected packet or
`expect: {drop: true}`. On mismatch, the report includes both packets as hex.

`files` can copy local fixtures into the guest before steps run. Its
`destination` is a guest path; `source` is relative to the manifest. Commands
are transported as `argv`, quoted by the runner, and never evaluated by the
host shell.

## Built-in scenarios

- `forward-route` validates the baseline forward/route pipeline with a UDP
  packet and an exact PCAP expectation.
- `decap` configures decapsulation prefixes, attaches the module, and inspects
  the resulting live state.
- `nat64` configures a prefix and mapping, attaches the module, and inspects its
  live state.

Run `reset` between unrelated experiments. Scenario execution is fail-fast and
writes its latest structured report to the session runtime directory under the
system temporary directory.

## Troubleshooting

Run `just lab doctor` first. If `up` fails, its error points to
`supervisor.log`, which contains the functional harness and QEMU startup logs.
Use `just lab report`, `just lab status`, and guest commands such as
`just lab exec -- ps aux` before resetting the VM. The runtime directory uses
mode `0700`; reports do not copy the host environment or other secret sources.

Development ideas and delivered milestones live in
[`lab/ROADMAP.md`](../lab/ROADMAP.md).
