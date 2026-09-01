---
name: code-cli
description: >-
  How a yanet CLI binary (yanet-cli-*) is written: the clap derive shape,
  typed arguments, the ync framework (connection, output, errors, exit codes,
  completion), the verb and flag dictionary, human and JSON rendering and the
  test policy. Load before creating or changing anything under cli/,
  modules|devices|objects|operators/*/cli/ or common/rust, and when reviewing
  such a diff.
---

# code-cli

A CLI is a thin, typed front for one gRPC service: clap proves the argument
form before a connection exists, the service owns every invariant,
`ync` owns connection, output, errors and completion. `references/new-crate.md`
has the manifest, `build.rs`, skeleton and registration steps for a new binary.

## Shape

- One crate per binary `yanet-cli-<suffix>`. `yanet-cli device <x>` and
  `yanet-cli operator <x>` dispatch to `yanet-cli-device-<x>` /
  `yanet-cli-operator-<x>` by name; the dispatcher needs no registration.
- `Cmd`: `#[derive(Debug, Clone, Parser)]`, `#[command(version, about)]`,
  `#[command(flatten_help = true)]`, fields `#[clap(subcommand)] mode: ModeCmd`,
  `#[command(flatten)] connection: ConnectionArgs`, `--format`
  (`CommonFormat`, `default_value = "human"`, `global = true`, doc
  `Output format.`) and `-v` (`ArgAction::Count`, `global = true`, doc
  `Be verbose: shows debug log lines and raw gRPC error details.`).
- `fn main() -> std::process::ExitCode` delegates the lifecycle to
  `ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)`. The scaffold
  owns completion before Tokio, parsing, output initialisation, the
  current-thread runtime, error rendering and the process status.
- `run(cmd)` determines the action, builds the service object once and matches
  `cmd.mode`; every handler returns `Result<(), Error>`.
- `const SERVICE_NAME: &str = "<proto package>.<Service>";` with the doc
  `/// The fully-qualified gRPC service name used in error messages.`. One
  service: `Service::connect_for(&cmd.connection, action, SERVICE_NAME,
  |channel| Client::new(channel).send_compressed(Gzip)
  .accept_compressed(Gzip)).await?`. Several:
  `Connection::connect_for(&cmd.connection, action).await?` once, then
  `Service::new(&connection, NAME, build)` per client. The action is the same
  user-facing verb passed to the command's RPC error mapping.
- Connection settings resolve inside `ync`, from flags, environment and the
  configuration file (`ync::config`). A CLI never reads
  `cmd.connection.endpoint` itself. A pre-connect error label comes from
  `client::resolve_label`, a post-connect one from `Service::endpoint()` /
  `Connection::endpoint()`.
- Every RPC: `self.service.client().<rpc>(request).await
  .map_err(self.service.status("<verb>"))?.into_inner()`.
- Generated code: `#[allow(clippy::all, clippy::std_instead_of_core,
  non_snake_case)] pub mod <x>pb { tonic::include_proto!("…"); }`; shared
  protos come through `extern_path` to `::commonpb::pb`.

## Arguments

- The parser proves the form: typed fields, `required = true` on a `Vec<T>`
  payload, `conflicts_with` for exclusive flags, `value_parser!(u16).range(..)`,
  `ValueEnum` for closed sets. A bad value exits 2 before any connection. No
  parsing in handlers, no `String` for a value that has a type.
- Type ladder, in order: `core`/`std` (`IpAddr`, `Ipv4Addr`, `Ipv6Addr`, `u16`
  for ports, `PathBuf`), then `netip` (`IpNetwork`, `Contiguous<IpNetwork>` for
  prefixes, `MacAddr`), then `common/rust` types with `FromStr`
  (`commonpb::pb::DevicePipeline`). Inside a crate only `ValueEnum` enums and
  clap arg groups. A `value_parser = <fn>` that constrains an existing type (a
  range, a fixed prefix length) is fine. A new `FromStr` type anywhere, in the
  crate or in `common/rust`, is a decision for the owner: stop and ask before
  writing it.
- Wire values are built with `From` when the request is made
  (`IpAddress::from(addr)`), never parsed from text there.
- Positional arguments carry the key of an element (`insert <prefix>
  --via …`, `remove <next-hop>…`, `table create <name>`) or the input
  document of an `update` (`update -n <name> <path>`); everything else is
  a flag.
- Flags: `--name/-n` is the config name and nothing else; `--category` a
  metric category (`--rules` retires into the positional update document
  under #2373); `-4/-6` family filters, mutually exclusive (nat64's
  address pair excepted); `--endpoint` and `--auth` exist only through
  `ConnectionArgs`. A short flag has one meaning across all binaries and
  never shadows the global `-v`, so no auto `short` on a subcommand flag.
- Verbs: configs `list / show / update` (upsert) `/ delete`; elements inside a
  config `insert` (upsert by key), `add` (strict or idempotent add to a set),
  `remove`; scalars `set-<x>`; the whole object `flush`. A rename keeps the old
  spelling as a hidden `alias` for a release.
- Every command and argument has a one-sentence doc comment ending in a
  period; `about` comes from the `Cmd` doc.
- An argument naming an existing object carries `add =
  ArgValueCandidates::new(<fn>)`, the function built on
  `completion::candidates(Cmd::command, build, async move |mut client| …)` (a
  genuine `async move` closure). Never on a `value_delimiter` argument, never
  on the new name of a `create`.

## Boundary with the service

The CLI checks presence, type, range and exclusivity. The configuration
itself, an empty config, a refcount, a duplicate key, a missing element on
remove, is the service's decision, relayed as its status. No confirmations,
no `--yes`, no `--dry-run`.

## Output

- Data leaves through `output::data(|| payload, || render)`. The JSON payload
  is the wire message or one of its fields, never a mirror struct. Human
  rendering is the closure.
- Lists: sort, then `display::print_table_from_entries(rows)` with `impl
  Tabled for <wire type>` (`LENGTH`, `fields` with `Cow::Borrowed` for `&str`
  and `Cow::Owned` for computed cells, `headers`). A local row struct only
  where the orphan rule blocks that (a type from a shared proto crate) or for
  a document schema that also parses a file. A single object is a key/value
  block, nested lists as sub-tables. Never a pretty-printed JSON, YAML or tree
  dump in human mode.
- Empty results: inside the render closure, `output::empty(…)` or
  `output::empty_with_hint(…, "create one with '<full command>'")` and an
  early return; never bare printing or a call-site guard. The primitive owns
  the marker and stays silent under JSON and on a non-TTY stdout.
- Mutations: `output::success("<verb>", format_args!("<Sentence>."))`.
- Colour and glyphs only via `output::is_colored()`, `output::dim`,
  `output::paint_dim`; no `colored` dependency in a CLI crate (#2377).
- An unusable derived JSON shape (an enum as a number) is fixed on the wire
  type: `field_attribute` in `build.rs` adds `#[serde(serialize_with = "…")]`,
  the function lives in `main.rs`, the exception is owner-approved.
- Ages, durations, sizes: `ync::humanfmt`; metrics: `ync::metrics`.

## Errors and exit codes

- Exit codes come only from `err.exit_code()`: 0 ok, 1 invalid argument, auth
  or RPC error, 2 usage (clap), 3 not found or service not registered, 4
  connection or unavailable. No command invents a code (`ready`'s 2 for
  "not ready" is legacy until #2354).
- A local rejection is `self.service.invalid("<verb>", message)` or
  `Error::invalid_argument(verb, endpoint, message)`; a command addressing an
  existing object maps its status through `NotFoundMapper::new(SERVICE_NAME,
  "<resource>")` and `.map(status, verb, endpoint, resource)`, so a missing
  object reads `<resource> not found` and exits 3. Hints via `.with_hint(…)`.

## Tests

Test the crate's own logic only: its formatters, a request mapping that merges
or branches, a file loader. Nothing that exercises clap (`debug_assert`,
`try_parse_from` for required, typed or conflicting arguments), `std` or
`netip` parsing, a JSON shape or human text; merged JSON-shape pins go when
their crate is next changed. Canonical MAC/hex and ASCII-path rendering are
contracts and a formatter is own logic — those tests stay. `fn
test_<what>_<case>` in `mod test`.

## Never

- Parse or validate after connecting.
- `String` in any wrapper for an address, MAC, prefix or pipeline.
- A `Vec<T>` payload without `required = true`.
- Accept `--format` and ignore it, or `println!` a JSON document.
- `CompleteEnv` inside a tokio runtime.
- Swallow a stream or writer error and exit 0.
- Heuristics over a library error: take the root of the `source()` chain.
