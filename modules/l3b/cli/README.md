# Configuration Documents

`create-service`, `update-service` and `update-module-config` retain their
current YAML documents and `--file` option. This is not a migration to a new
protobuf API or ProtoJSON format. Existing field names, default values, wire
requests and native network parsing remain unchanged.

Unknown fields at L3B-owned nesting levels are ignored by default, including
null values and entire unknown subtrees. Warnings on stderr identify their
field paths, never their values; paths are escaped and truncated after 256
characters, and at most 16 paths plus a summary are reported per document.
Machine-readable stdout is unchanged. An ignored field is not sent to the
server. Existing snake_case names remain the supported names; camelCase
spellings are not aliases.

Known fields still reject invalid types and values. Duplicate keys, YAML merge
keys, custom tags and alias references are rejected throughout the document,
including ignored subtrees. Mapping keys must be scalars. Null members of known
lists remain invalid. Unknown values have no known field schema and are not
subject to the integer rules below. Comments, quoted values and native network
values are not scanned as YAML syntax.

Known integer fields require unquoted nonnegative decimal scalars made of ASCII
digits and within the field's existing range. Hexadecimal, octal, binary,
signed, floating-point, boolean and quoted numeric forms are rejected. Leading
zero forms that YAML treats as strings remain invalid for integer fields.
Service names in destination rules must be string scalars, not numbers or
booleans implicitly converted to strings. This does not impose new service-name
or address restrictions.

Existing optional nulls remain supported: `dscp: null` means no marking, and
null protocol subtype endpoints select their existing defaults. In contrast,
`session_timeouts: null` is not a valid structure; omit it or use `{}` for the
current defaults. Imported native network semantics are unchanged.

Invalid documents produce an invalid-input failure with their source file and
location or field path and do not send a mutation RPC. A connection may already
have been opened before parsing by an operational command.

## Strict Configuration CI

Use the explicit offline validation commands to reject unknown fields:

```sh
yanet-cli l3b validate-service service.yaml
yanet-cli l3b validate-module-config module.yaml
```

These commands require a document and use the same loaders and wire conversion
as the operational commands. They do not read connection configuration files,
resolve endpoints, contact a Gateway or send any RPC. The shared CLI parser
still requires valid flag and environment-variable syntax: for example,
`--timeout 0` or `YANET_TIMEOUT=garbage` is a usage error (exit 2) before document
validation. Valid connection options are otherwise unused. Success exits 0;
invalid documents exit 1, with the usual human or `--format json` output. This
checks local document validity, not server state or deployment readiness. CI
must use a binary/schema that knows the fields being introduced.

## Configuration-First Rollout

Additive configuration can precede supporting binaries only after this
compatibility policy has reached the binaries applying it. Already released
strict binaries still reject unknown fields until upgraded; this change does
not fix them retroactively.

An unsupported field is ignored when the configuration is applied, not stored
for later activation. It takes effect only when a supporting binary reapplies
the configuration. Ignoring a security- or routing-critical requirement is not
proof that it is enforced: verify enforcement before relying on it.

Renaming or removing existing fields, or changing their meaning, is not made
compatible by ignoring additions. Compatibility between a new CLI and an old
backend is a separate concern; this policy does not negotiate backend features.
