# New CLI crate

Everything a new `yanet-cli-<suffix>` binary needs, in the order it is
written. `<x>` is the module, device or operator name, `<x>pb` its proto
package crate name (`decappb`, `operatorpb`, …).

## Directory

```
<owner>/<x>/cli/
  Cargo.toml
  build.rs
  src/main.rs
```

`<owner>` is `modules`, `devices`, `objects` or `operators`; a core binary
lives in `cli/modules/<x>/`.

## Cargo.toml

```toml
[package]
name = "yanet-cli-<suffix>"
version = "0.1.0"
edition = "2024"
publish = false

rust-version = "1.88"

[lints]
workspace = true

[dependencies]
commonpb = { path = "../../../common/rust/commonpb", version = "0.1", package = "yanet-commonpb" }
ync = { path = "../../../cli/core", version = "0.1", package = "yanet-cli" }
clap = { version = "4.5", features = ["derive", "wrap_help"] }
clap_complete = { version = "4.5", features = ["unstable-dynamic"] }
netip = "0.3"
prost = "0.14"
serde = { version = "1", features = ["derive"] }
tabled = { version = "0.21", default-features = false, features = ["ansi", "derive"] }
tonic = { version = "0.14", features = ["gzip"] }
tonic-prost = "0.14"

[build-dependencies]
tonic-prost-build = { version = "0.14", default-features = false, features = ["transport"] }
```

Add a dependency only when the code uses it; `netip` and `tabled` are
listed because almost every binary parses an address or prints a table.
`tabled` keeps its default features off, since the default `assert`
feature links `testing_table` into the binary, and lists `derive` only
when a row type derives `Tabled`.

`tonic-prost-build` keeps its default features off: the default
`cleanup-markdown` re-renders proto doc comments and fails clippy's
`doc_lazy_continuation` on the generated code, while `transport` keeps the
generated `connect` constructors that tonic-build 0.13 always emitted.

## build.rs

```rust
use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    let root = "../../..";
    // modules, devices and objects; an operator's protos live under
    // operators/<x>/operatorpb/v1/ instead, with file names of their
    // own (route.proto, operator.proto, …) — check the tree.
    let proto = "<owner>/<x>/controlplane/<x>pb/v1/<x>.proto";
    println!("cargo:rerun-if-changed={root}/{proto}");

    tonic_prost_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize)]")
        // One line per enum field that must render by its proto name.
        .field_attribute(".<package>.<Message>.<field>", "#[serde(serialize_with = \"crate::serialize_<field>\")]")
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .compile_protos(&[format!("{root}/{proto}")], &[root])?;

    Ok(())
}
```

Shared packages (`common.commonpb.v1`, `common.filterpb.v1`) are always
`extern_path`ed to their crate; never compile them twice.

## src/main.rs

```rust
//! CLI for YANET <x>.

use std::borrow::Cow;

use clap::{CommandFactory, Parser};
use clap_complete::{
    engine::{ArgValueCandidates, CompletionCandidate},
};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    display::print_table_from_entries,
    errors::Error,
    output,
};

use crate::<x>pb::{
    Config, DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    <x>_service_client::<X>ServiceClient,
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod <x>pb {
    tonic::include_proto!("<proto package>");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "<proto package>.<X>Service";

fn client(channel: LayeredChannel) -> <X>ServiceClient<LayeredChannel> {
    <X>ServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages <x> module configs.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List configs.
    List,
    /// Show a config.
    Show(ShowCmd),
    /// Create or replace a config.
    Update(UpdateCmd),
    /// Delete a config.
    Delete(DeleteCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Update(..) => "update",
            Self::Delete(..) => "delete",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Name of the <x> config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Name of the <x> config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Prefixes to install.
    #[arg(long = "prefix", short = 'p', required = true)]
    pub prefixes: Vec<netip::Contiguous<netip::IpNetwork>>,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// Name of the <x> config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = <X>Service::new(&cmd.globals.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(args) => service.show_config(args).await,
        ModeCmd::Update(args) => service.update_config(args).await,
        ModeCmd::Delete(args) => service.delete_config(args).await,
    }
}

pub struct <X>Service {
    service: Service<<X>ServiceClient<LayeredChannel>>,
}

impl <X>Service {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, client).await?;

        Ok(Self { service })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .unary("list", ListConfigsRequest {}, async |client, request| {
                client.list_configs(request).await
            })
            .await?;

        output::data(
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No configs found."),
                        format_args!("create one with 'yanet-cli-<suffix> update --name <name> …'"),
                    );
                    return;
                }

                let mut entries: Vec<&Config> = response.configs.iter().collect();
                entries.sort_by(|a, b| a.name.cmp(&b.name));
                print_table_from_entries(entries);
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };

        let response = self
            .service
            .unary_with(
                "show",
                request,
                self.service.not_found("show", &format!("config '{}'", cmd.config_name)),
                async |client, request| client.show_config(request).await,
            )
            .await?;

        output::data(
            || &response,
            || {
                println!("name:     {}", response.name);
                println!("prefixes: {}", response.prefixes.len());
            },
        );

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            prefixes: cmd.prefixes.iter().map(|prefix| prefix.clone().into()).collect(),
        };

        self.service
            .unary("update", request, async |client, request| client.update_config(request).await)
            .await?;

        output::success("update", format_args!("Updated config '{}'.", cmd.config_name));

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };

        self.service
            .unary_with(
                "delete",
                request,
                self.service.not_found("delete", &format!("config '{}'", cmd.config_name)),
                async |client, request| client.delete_config(request).await,
            )
            .await?;

        output::success("delete", format_args!("Deleted config '{}'.", cmd.config_name));

        Ok(())
    }
}

impl Tabled for Config {
    const LENGTH: usize = 2;

    fn fields(&self) -> Vec<Cow<'_, str>> {
        vec![
            Cow::Borrowed(self.name.as_str()),
            Cow::Owned(self.prefixes.len().to_string()),
        ]
    }

    fn headers() -> Vec<Cow<'static, str>> {
        vec![Cow::Borrowed("NAME"), Cow::Borrowed("PREFIXES")]
    }
}

/// Completion candidates for a config-name argument: the configs the
/// service currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        client,
        async move |mut client| {
            Ok(client
                .list_configs(ListConfigsRequest {})
                .await?
                .into_inner()
                .configs
                .into_iter()
                .map(|config| config.name)
                .collect())
        },
    )
}
```

Several services behind one binary:
`Connection::connect_for(connection, action).await?` once, then
`Service::new(&connection, NAME, build)` for each client, all kept in the
service struct.

## Registration

Three files move together; missing the last two builds green and never
installs the binary.

1. Root `Cargo.toml`: add the crate path to `[workspace] members`.
2. Root `Makefile`: add the suffix (`<x>`, `device-<x>`, `operator-<x>`) to
   `CLI_MODULES`; a `cli/modules/<x>` crate goes to `CLI_CORE_MODULES`.
3. `debian/yanet2-cli.install`: add `usr/bin/yanet-cli-<suffix>`.

Shell completions need nothing: the packaging script emits
`COMPLETE=bash <bin>` for every installed `yanet-cli-*`.

## Gates before review

```bash
cargo +nightly-2026-08-28 fmt -p yanet-cli-<suffix>
cargo clippy -p yanet-cli-<suffix> --all-targets
cargo build --release -p yanet-cli-<suffix>
cargo test -p yanet-cli-<suffix>
```

Then run the binary against a closed port (`--endpoint grpc://127.0.0.1:1`):
a malformed argument must exit 2 with clap's message, a valid one must reach
`<action> failed` and exit 4; `--help` must describe every flag; `--format json`
must print the wire message.
