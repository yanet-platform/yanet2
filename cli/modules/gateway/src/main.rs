//! CLI for the YANET gateway service registry.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use tabled::{
    Table, Tabled,
    settings::{
        Color, Style,
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
    },
};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{BackendKind, ListServicesRequest, RegisteredBackend, gateway_client::GatewayClient};

const GATEWAY_SERVICE: &str = "controlplane.ynpb.v1.Gateway";

/// Gateway - inspects the gateway service registry.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all services registered with the gateway.
    List,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);
    colored::control::set_override(output::is_colored());

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = GatewayService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_services().await,
    }
}

pub struct GatewayService {
    client: GatewayClient<LayeredChannel>,
    endpoint: String,
}

impl GatewayService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|err| Error::from_connection(err, "gateway", connection.endpoint.clone()))?;
        let client = GatewayClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);

        Ok(Self {
            client,
            endpoint: connection.endpoint.clone(),
        })
    }

    pub async fn list_services(&mut self) -> Result<(), Error> {
        let response = self
            .client
            .list_services(ListServicesRequest {})
            .await
            .map_err(|status| Error::from_status(status, "gateway", self.endpoint.clone(), GATEWAY_SERVICE))?
            .into_inner();

        let rows: Vec<ServiceRow> = response.services.iter().map(ServiceRow::from).collect();

        output::data(
            &response.services,
            rows.is_empty(),
            format_args!("no services registered"),
            || render_table(&rows),
        );

        Ok(())
    }
}

/// A displayable row for the gateway services table.
#[derive(Debug, Tabled, serde::Serialize)]
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
        BackendKind::External | BackendKind::Unspecified => format_age(ts),
    }
}

fn render_table(rows: &[ServiceRow]) {
    let mut table = Table::new(rows);
    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_horizontal(),
    );

    if output::is_colored() {
        table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
        table.modify(Rows::first(), Color::BOLD);
    }

    ync::display::fit_terminal_width(&mut table);
    println!("{table}");
}

/// Formats a `prost_types::Timestamp` as a human-readable relative age.
///
/// Returns `"-"` when `ts` is `None` or the zero sentinel
/// (`seconds == 0 && nanos == 0`). Otherwise formats as `Xs ago`,
/// `XmYs ago`, or `XhYm ago` depending on magnitude.
pub fn format_age(ts: Option<&prost_types::Timestamp>) -> String {
    let ts = match ts {
        Some(ts) if ts.seconds != 0 || ts.nanos != 0 => ts,
        _ => return "-".to_string(),
    };

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default();

    let ts_secs = ts.seconds.max(0) as u64;
    let now_secs = now.as_secs();

    let secs = now_secs.saturating_sub(ts_secs);

    if secs < 60 {
        format!("{secs}s ago")
    } else if secs < 3600 {
        let minutes = secs / 60;
        let remainder = secs % 60;
        format!("{minutes}m{remainder}s ago")
    } else {
        let hours = secs / 3600;
        let minutes = (secs % 3600) / 60;
        format!("{hours}h{minutes}m ago")
    }
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn format_age_none_returns_dash() {
        assert_eq!("-", format_age(None));
    }

    #[test]
    fn format_age_zero_sentinel_returns_dash() {
        let ts = prost_types::Timestamp { seconds: 0, nanos: 0 };
        assert_eq!("-", format_age(Some(&ts)));
    }

    #[test]
    fn format_age_seconds() {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap();
        let ts = prost_types::Timestamp {
            seconds: now.as_secs() as i64 - 30,
            nanos: 0,
        };
        assert_eq!("30s ago", format_age(Some(&ts)));
    }

    #[test]
    fn format_age_minutes() {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap();
        let ts = prost_types::Timestamp {
            seconds: now.as_secs() as i64 - 64,
            nanos: 0,
        };
        assert_eq!("1m4s ago", format_age(Some(&ts)));
    }

    #[test]
    fn format_age_hours() {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap();
        let ts = prost_types::Timestamp {
            seconds: now.as_secs() as i64 - (2 * 3600 + 3 * 60),
            nanos: 0,
        };
        assert_eq!("2h3m ago", format_age(Some(&ts)));
    }

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
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap();
        let ts = prost_types::Timestamp {
            seconds: now.as_secs() as i64 - 5,
            nanos: 0,
        };
        assert_eq!("5s ago", last_seen_cell(BackendKind::External, Some(&ts)));
    }

    #[test]
    fn last_seen_cell_unspecified_shows_age() {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap();
        let ts = prost_types::Timestamp {
            seconds: now.as_secs() as i64 - 5,
            nanos: 0,
        };
        assert_eq!("5s ago", last_seen_cell(BackendKind::Unspecified, Some(&ts)));
    }
}
