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

[[bin]]
name = "yanet-cli-<suffix>"
path = "src/main.rs"

[dependencies]
commonpb = { path = "../../../common/rust/commonpb", version = "0.1", package = "yanet-commonpb" }
ync = { path = "../../../cli/core", version = "0.1", package = "yanet-cli" }
clap = { version = "4.5", features = ["derive", "wrap_help"] }
clap_complete = { version = "4.5", features = ["unstable-dynamic"] }
netip = "0.3"
prost = "0.13"
serde = { version = "1", features = ["derive"] }
tabled = { version = "0.18", features = ["ansi"] }
tokio = { version = "1", features = ["rt", "net", "time", "macros", "sync"] }
tonic = { version = "0.13", features = ["gzip"] }

[build-dependencies]
tonic-build = "0.13"
```

Add a dependency only when the code uses it; `netip` and `tabled` are
listed because almost every binary parses an address or prints a table.

## build.rs

```rust
use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    let root = "../../..";
    let proto = "<owner>/<x>/controlplane/<x>pb/v1/<x>.proto";
    println!("cargo:rerun-if-changed={root}/{proto}");

    tonic_build::configure()
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

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::{
    CompleteEnv,
    engine::{ArgValueCandidates, CompletionCandidate},
};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    display::print_table_from_entries,
    errors::{Error, NotFoundMapper},
    output::{self, CommonFormat},
};

use crate::<x>pb::{
    Config, DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    <x>_service_client::<X>ServiceClient,
};

#[allow(clippy::all, clippy::std_instead_of_core, non_snake_case)]
pub mod <x>pb {
    tonic::include_proto!("<proto package>");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "<proto package>.<X>Service";

/// Maps a genuine "config not found" status into a friendly message.
const NOT_FOUND: NotFoundMapper = NotFoundMapper::new(SERVICE_NAME, "config");

/// <X> CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
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

fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    start();
}

#[tokio::main(flavor = "current_thread")]
async fn start() {
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = <X>Service::new(&cmd.connection).await?;

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
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            <X>ServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .client()
            .list_configs(ListConfigsRequest {})
            .await
            .map_err(self.service.status("list"))?
            .into_inner();

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
            .client()
            .show_config(request)
            .await
            .map_err(|status| NOT_FOUND.map(status, "show", self.service.endpoint(), Some(&cmd.config_name)))?
            .into_inner();

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
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated config {}.", cmd.config_name));

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };

        self.service
            .client()
            .delete_config(request)
            .await
            .map_err(|status| NOT_FOUND.map(status, "delete", self.service.endpoint(), Some(&cmd.config_name)))?;

        output::success("delete", format_args!("Deleted config {}.", cmd.config_name));

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
        |channel| {
            <X>ServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
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

Several services behind one binary: `Connection::connect(connection).await?`
once, then `Service::new(&connection, NAME, build)` for each client, all
kept in the service struct.

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
`connect failed` and exit 4; `--help` must describe every flag; `--format json`
must print the wire message.
