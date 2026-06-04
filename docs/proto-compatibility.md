# Protobuf API Compatibility Policy

All gRPC/protobuf APIs in YANET2 are versioned with a `.v1` fully-qualified package name — enforced by directory layout and the custom `go_package` linter. This document defines what is frozen, what kinds of changes are safe, and what is forbidden, with the `buf breaking` CI gate ensuring no wire-incompatible change can land on `main`.

## API Surface Inventory

The following versioned package families are covered by the breaking gate.

**Gateway core** — `controlplane/ynpb/v1/`
- Package prefix: `controlplane.ynpb.v1`

**Shared types** — `common/*/v1/`
- `common.commonpb.v1` — IP addresses, ranges, MAC addresses, metrics, targets
- `common.filterpb.v1` — packet filter types
- `common.readinesspb.v1` — readiness probes

**Per-module services** — `modules/<name>/controlplane/<name>pb/v1/`
- `modules.acl.controlplane.aclpb.v1`
- `modules.decap.controlplane.decappb.v1`
- `modules.dscp.controlplane.dscppb.v1`
- `modules.forward.controlplane.forwardpb.v1`
- `modules.fwstate.controlplane.fwstatepb.v1`
- `modules.nat64.controlplane.nat64pb.v1`
- `modules.pdump.controlplane.pdumppb.v1`
- `modules.route.controlplane.routepb.v1`
- `modules.route_mpls.controlplane.routemplspb.v1` (directory `route-mpls`, package `route_mpls` — see Exclusions)

**Devices** — `devices/<name>/controlplane/<name>pb/v1/`
- `devices.plain.controlplane.plainpb.v1`
- `devices.vlan.controlplane.vlanpb.v1`

**Operators** — `operators/<name>/.../v1/`
- `operators.pipeline.operatorpb.v1`
- `operators.route.operatorpb.v1`
- `operators.forward.operatorpb.v1`
- `operators.bird_adapter.adapterpb.v1` (directory `bird-adapter`, package `bird_adapter` — see Exclusions)

`modules/balancer2` (`modules.balancer2.controlplane.balancerpb.v1`) is **pre-v1** and explicitly excluded from the breaking gate. See Freeze Status.

## Package and Layout Conventions

The package name is derived from the file's repo-relative directory path, ending in `.v1`. The proto source lives in a `v1/` subdirectory. The `go_package` option follows the pattern:

```protobuf
option go_package = "github.com/yanet-platform/yanet2/<dir-path>/v1;<alias>";
```

The alias (e.g., `ynpb`, `aclpb`, `routemplspb`) keeps the Go package identifier stable so existing consumers only gain a `/v1` suffix in the import path — they do not need to rename any Go symbols. Shared protos are compiled with `-I <repo-root>` so they register under their full repo-relative path and can be imported across modules.

## Additive (Safe) Changes

The following changes are backwards-compatible and pass the gate:

- Adding a new field with a previously unused field number.
- Adding a new `message` or `enum` type.
- Adding new values to an existing `enum` (with a new number).
- Adding a new RPC method to an existing service.
- Adding a new `service`.

## Breaking (Forbidden) Changes

The following changes are wire-incompatible and are blocked by the gate on any frozen `.v1` package:

- Changing or reusing a field number.
- Changing a field's type or cardinality (e.g., `string` → `bytes`, singular → repeated).
- Renaming or removing a field, message, enum value, RPC, or service.
- Renaming the package itself.

The package and service name together form the gateway's HTTP-to-gRPC wire route (`POST /api/<package>.<Service>/<Method>`). A rename therefore breaks the Rust CLI clients, the web client, and the auth permissions file at `controlplane/etc/yanet/auth/permissions.yaml` simultaneously — and must never happen in place. When a genuinely incompatible evolution is required, create a new versioned package (`.v2` in a `v2/` directory) rather than modifying the existing one.

## Deprecating and Removing Fields

The gate's `PACKAGE` category includes `FIELD_NO_DELETE` and related `*_NO_DELETE` rules, which means deleting a field, message, enum value, RPC, or service from a frozen `.v1` package fails `make proto-breaking` — even if its number and name are subsequently marked `reserved`. Deletion is therefore forbidden in place; the only compliant way to retire a field within an existing `.v1` package is to keep it on the wire and mark it deprecated:

```protobuf
string legacy_name = 3 [deprecated = true]; // Use new_name instead.
```

Keep the field declaration present so existing serialized data continues to decode correctly and so the gate does not fire.

The `reserved` keyword is still useful in two narrower cases. First, when carving out a new versioned package (`.v2` in a `v2/` directory) you may drop fields that existed in v1; reserve their old numbers and names in the v2 message so they can never be accidentally recycled:

```protobuf
message Foo {
  reserved 3;
  reserved "old_field";
}
```

Second, `reserved` is appropriate for field numbers that were removed from a package before its v1 freeze, where no `*_NO_DELETE` violation can occur because the deletion predates the gate baseline. In both cases the rule is the same: never reuse a field number, even for a field of the same type.

## What the Gate Enforces

`buf breaking` runs on every pull request, comparing the branch against `main` using the `PACKAGE` breaking-change category configured in `buf.yaml`. The `PACKAGE` ruleset catches wire-incompatible changes at the package, file, message, field, enum, and RPC level — covering all cases described above. Critically, it is additive-only for existing packages: it blocks deleting fields, enum values, messages, RPCs, and services (via the `*_NO_DELETE` rules) in addition to catching type and number changes, so the only safe in-place evolution of a frozen `.v1` package is adding new elements. For the full rule taxonomy, see the [buf breaking overview](https://buf.build/docs/breaking/overview).

In CI (`proto-lint.yml`), the `buf-breaking` job runs only on pull requests (not on direct pushes to `main`), comparing against `https://github.com/yanet-platform/yanet2.git#branch=main`.

## Documented Exclusions and Rule Exceptions

The following entries in `buf.yaml` intentionally deviate from the defaults. Each has a specific reason.

**`modules.excludes`**
- `subprojects` — vendored DPDK source; not part of the YANET API surface.
- `.claude` — agent worktree directories; not source files.

**`breaking.ignore`**
- `modules/balancer2` — pre-v1 rewrite in flight; API shape is not yet stable. Remove this line from `buf.yaml` when balancer2 reaches v1.

**`lint.except`** — six identifier-naming rules are disabled globally because enforcing them would require renaming existing wire identifiers, which would itself be a breaking change:
- `ENUM_VALUE_PREFIX`
- `ENUM_ZERO_VALUE_SUFFIX`
- `RPC_REQUEST_STANDARD_NAME`
- `RPC_RESPONSE_STANDARD_NAME`
- `RPC_REQUEST_RESPONSE_UNIQUE`
- `SERVICE_SUFFIX`

**`lint.ignore_only`**
- `PACKAGE_DIRECTORY_MATCH` for `modules/route-mpls` and `operators/bird-adapter` — hyphenated directory names cannot match the underscored package identifier that protobuf requires (`route_mpls`, `bird_adapter`). The mismatch is structural and cannot be resolved without a rename.
- `ENUM_VALUE_UPPER_SNAKE_CASE` for `common/filterpb` — the `FragmentKind` enum has legacy wire values (`Any`, `None`, `Frag`) that use mixed case. Renaming them to screaming snake case would change the wire names and break existing serialized data.

## Freeze Status

All surfaces listed in the API Surface Inventory are frozen as of the `buf breaking` gate landing. The CI gate prevents regressions from that point forward.

`modules/balancer2` is explicitly **pre-v1**: its proto package exists but is excluded from the breaking gate while its rewrite is in flight. The concrete milestone that marks balancer2 reaching v1 is removing the `modules/balancer2` line from `breaking.ignore` in `buf.yaml`.

## Running the Checks Locally

Requires `buf` to be installed ([installation guide](https://buf.build/docs/installation)). Both targets soft-skip with a warning if `buf` is absent.

```bash
# Run buf lint + the custom go_package linter
make proto-lint

# Run buf breaking against main
make proto-breaking
```

`make proto-lint` also runs the custom Go-based `protolint` tool that checks `go_package` alignment across all protos. `make proto-breaking` invokes `buf breaking --against ".git#branch=main"` — run it on a feature branch to catch issues before opening a PR.

## PR Author Checklist

- [ ] Editing an existing `.v1` package? Only add new fields, messages, enums, or RPCs — never renumber, retype, rename, or remove.
- [ ] Retiring a field? Keep the declaration and mark it `[deprecated = true]` with a comment pointing at the replacement. Do NOT delete the field from a frozen `.v1` package — the gate forbids it. Reserve old numbers and names only when creating a new `v2` package.
- [ ] Ran `make proto-lint` locally and it passed with zero errors.
- [ ] Ran `make proto-breaking` locally and it passed with zero errors.
- [ ] Added a new RPC or service? Updated all consumers: Go generated code, Rust CLI `build.rs`, web client, and `controlplane/etc/yanet/auth/permissions.yaml`.
- [ ] Need an incompatible change? Open a discussion about introducing a `.v2` package in a `v2/` directory instead of modifying the existing one.
