# Configuration Documents

`create-service`, `update-service` and `update-module-config` retain their
current YAML documents and `--file` option. This is not a migration to a new
protobuf API or ProtoJSON format. Existing field names, default values, wire
requests and native network parsing remain unchanged.

Documents are strict: unknown fields (even when null), duplicate keys, YAML
merge keys, custom tags, alias references and null list members are rejected.
Only the existing snake_case field names are accepted; there are no additional
camelCase aliases. Comments, quoted strings and native network values are not
scanned as YAML syntax.

Integer fields require unquoted nonnegative decimal scalars made of ASCII
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

These are deliberate input-validation incompatibilities: documents previously
accepted by ignoring unknown fields or normalizing disallowed YAML must be
corrected. Invalid documents produce an invalid-input failure with their source
file and location and do not send a mutation RPC. A connection may already have
been opened before document parsing.
