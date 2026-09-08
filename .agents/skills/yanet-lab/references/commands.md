# Command and manifest reference

The canonical entry point is `just lab [--session NAME] [--json] <command>`.
Bare `just lab` prints help without starting a VM. `up` and `status` also accept
the session name positionally: `just lab up NAME` and `just lab status NAME`.

- Lifecycle: `doctor`, `up`, `status`, `reset`, `down`.
- Guest access: `exec -- COMMAND ARG...`, `shell`, `serial` (press `Ctrl-]` to detach).
- Diagnostics: `report`, optionally with global `--json`.
- Guided flows: `scenario list`, `scenario run NAME`.
- Custom flows: `manifest validate PATH`, `manifest run PATH`.

Manifests use `version: 1` and are validated against
`lab/yanet2-lab.schema.json`. Keep `$schema` relative to the manifest. Local
file and PCAP paths must be relative and remain inside the manifest directory.
Only interfaces `0` and `1` exist in the initial topology. Probe expectations
are either `pcap: path` or `drop: true`, never both. Fixture files are limited
to 64 KiB, and packet captures use Ethernet framing.

JSON commands write one JSON value to stdout on success or failure. Failure
commands leave stderr empty and exit non-zero. The supervisor accepts one
operation at a time; concurrent commands return `lab is busy` instead of being
queued.
