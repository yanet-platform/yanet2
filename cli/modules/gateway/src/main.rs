//! CLI for the YANET gateway service registry.

use std::time::SystemTime;

use clap::Parser;
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    errors::Error,
    humanfmt, output,
};
use ynpb::pb::{BackendKind, ListServicesRequest, RegisteredBackend, gateway_client::GatewayClient};

const GATEWAY_SERVICE: &str = "controlplane.ynpb.v1.Gateway";

/// Inspects the gateway service registry.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all services registered with the gateway.
    List,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = Service::connect_for(&cmd.globals.connection, "gateway", GATEWAY_SERVICE, |channel| {
        GatewayClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip)
    })
    .await?;

    match cmd.mode {
        ModeCmd::List => list_services(&mut service).await,
    }
}

type GatewayService = Service<GatewayClient<LayeredChannel>>;

async fn list_services(service: &mut GatewayService) -> Result<(), Error> {
    let response = service
        .unary("gateway", ListServicesRequest {}, async |client, request| {
            client.list_services(request).await
        })
        .await?;

    output::data(
        || &response.services,
        || {
            let rows: Vec<ServiceRow> = response.services.iter().map(ServiceRow::from).collect();

            if rows.is_empty() {
                output::empty(format_args!("No services registered."));
                return;
            }

            ync::display::print_table_from_entries(&rows);
        },
    );

    Ok(())
}

/// A displayable row for the gateway services table.
#[derive(Debug, Tabled)]
pub struct ServiceRow {
    #[tabled(rename = "Name")]
    pub name: String,
    #[tabled(rename = "Kind")]
    pub kind: String,
    #[tabled(rename = "Endpoint")]
    pub endpoint: String,
    #[tabled(rename = "Last seen")]
    pub last_seen: String,
}

impl From<&RegisteredBackend> for ServiceRow {
    fn from(backend: &RegisteredBackend) -> Self {
        let (name, endpoint) = backend
            .backend
            .as_ref()
            .map(|b| (b.name.clone(), b.endpoint.clone()))
            .unwrap_or_default();

        let kind = BackendKind::try_from(backend.kind).unwrap_or(BackendKind::Unspecified);

        Self {
            name,
            kind: kind_display(kind),
            endpoint,
            last_seen: last_seen_cell(kind, backend.last_seen_at.as_ref()),
        }
    }
}

/// Returns the human-readable label for a `BackendKind`.
fn kind_display(kind: BackendKind) -> String {
    match kind {
        BackendKind::Builtin => "built-in".to_string(),
        BackendKind::InProcess => "in-process".to_string(),
        BackendKind::External => "external".to_string(),
        BackendKind::Unspecified => "unspecified".to_string(),
    }
}

/// Returns the last-seen cell value for a row.
///
/// `Builtin` and `InProcess` register once and never heartbeat, so showing an
/// age would be misleading — those arms return `—`. `External` backends
/// heartbeat and always show the age. `Unspecified` also shows the age: when a
/// newer CLI talks to an older gateway that predates the `kind` field, proto3
/// decodes every backend's `kind` as `Unspecified`, so falling back to
/// `format_age` preserves the pre-kind staleness signal for external services.
fn last_seen_cell(kind: BackendKind, ts: Option<&prost_types::Timestamp>) -> String {
    match kind {
        BackendKind::Builtin | BackendKind::InProcess => "\u{2014}".to_string(),
        BackendKind::External | BackendKind::Unspecified => {
            humanfmt::format_age(ts, SystemTime::now()).unwrap_or_else(|| "-".to_string())
        }
    }
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn kind_display_all_variants() {
        assert_eq!("built-in", kind_display(BackendKind::Builtin));
        assert_eq!("in-process", kind_display(BackendKind::InProcess));
        assert_eq!("external", kind_display(BackendKind::External));
        assert_eq!("unspecified", kind_display(BackendKind::Unspecified));
    }

    #[test]
    fn last_seen_cell_builtin_shows_em_dash() {
        let ts = prost_types::Timestamp { seconds: 1_000_000, nanos: 0 };
        assert_eq!("\u{2014}", last_seen_cell(BackendKind::Builtin, Some(&ts)));
    }

    #[test]
    fn last_seen_cell_in_process_shows_em_dash() {
        let ts = prost_types::Timestamp { seconds: 1_000_000, nanos: 0 };
        assert_eq!("\u{2014}", last_seen_cell(BackendKind::InProcess, Some(&ts)));
    }

    #[test]
    fn last_seen_cell_external_shows_age() {
        let now = SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap();
        let ts = prost_types::Timestamp {
            seconds: now.as_secs() as i64 - 5,
            nanos: 0,
        };
        assert_ne!("\u{2014}", last_seen_cell(BackendKind::External, Some(&ts)));
    }

    #[test]
    fn last_seen_cell_unspecified_shows_age() {
        let now = SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap();
        let ts = prost_types::Timestamp {
            seconds: now.as_secs() as i64 - 5,
            nanos: 0,
        };
        assert_ne!("\u{2014}", last_seen_cell(BackendKind::Unspecified, Some(&ts)));
    }
}
