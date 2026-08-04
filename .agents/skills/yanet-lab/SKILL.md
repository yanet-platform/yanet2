---
name: yanet-lab
description: Operate and troubleshoot the reusable local YANET2 QEMU lab. Use when asked to boot or reset a lab VM, inspect YANET in a guest, validate or author lab manifests, run forward-route/decap/NAT64 scenarios, execute packet probes, or collect a lab report.
---

# YANET2 Lab

Use the repository's `just lab` entry point. The CLI owns manifest validation
and QEMU lifecycle; do not reproduce those mechanics in ad-hoc scripts.

## Workflow

1. Run `just lab doctor` before booting when host readiness is unknown.
2. Run `just lab up`, then `just lab status`.
3. Prefer a built-in scenario when it covers the request:
   `just lab scenario list` and `just lab scenario run <name>`.
4. For a custom manifest, run `just lab manifest validate <path>` before
   `just lab manifest run <path>`.
5. On failure, collect `just lab report` and inspect the supervisor log path
   printed by `up`. Use `just lab exec -- <command> [args...]` for focused
   guest diagnostics.
6. Use `just lab reset` before an unrelated experiment. Run `just lab down`
   when the user no longer needs the VM.

## Guardrails

- Treat `reset` and `down` as state-changing operations. Do not run them while
  the user is investigating state unless requested or clearly necessary.
- Keep manifest commands as argv arrays. Never replace them with host shell
  interpolation.
- Put scenario paths and packet fixtures beside the scenario manifest.
- Validate a manifest after every edit.
- Use `--json` when another tool needs structured output.

## Reference

Read [the command reference](references/commands.md) when choosing diagnostics
or editing a manifest. The full human guide is `docs/lab.md`; future work and
accepted limitations are tracked in `lab/ROADMAP.md`.
