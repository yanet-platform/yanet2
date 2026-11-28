use core::{cell::Cell, fmt, time::Duration};
use std::path::PathBuf;

use clap::{CommandFactory, Parser, Subcommand};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use nat64spb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, ShowSessionsRequest, UpdateConfigRequest,
    nat64s_service_client::Nat64sServiceClient, nat64s_service_server::SERVICE_NAME,
};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{self, LayeredChannel, Service},
    completion, display,
    errors::Error,
    output, yaml,
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod nat64spb {
    tonic::include_proto!("modules.nat64s.controlplane.nat64spb.v1");
}

fn client(channel: LayeredChannel) -> Nat64sServiceClient<LayeredChannel> {
    Nat64sServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Zstd)
        .accept_compressed(CompressionEncoding::Zstd)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages stateful NAT64 (nat64s) module configs.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Subcommand)]
pub enum ModeCmd {
    /// List all stateful NAT64 configurations.
    List,
    /// Show current configuration.
    Show(ShowConfigCmd),
    /// Create or replace a config.
    Update(UpdateConfigCmd),
    /// Delete a config.
    Delete(DeleteConfigCmd),
    /// Show the live sessions of a config.
    Sessions(ShowSessionsCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Update(..) => "update",
            Self::Delete(..) => "delete",
            Self::Sessions(..) => "sessions",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowSessionsCmd {
    /// The name of the config whose sessions to show.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Sessions per page requested from the control plane.
    #[arg(long = "page-size", value_parser = clap::value_parser!(u32).range(1..), default_value_t = 1024)]
    pub page_size: u32,
    /// Show only the first sessions of the table.
    #[arg(long = "limit", value_parser = clap::value_parser!(u64).range(1..))]
    pub limit: Option<u64>,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateConfigCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Path to the module config file: the update request in YAML.
    ///
    /// The file spells the wire request, exactly what `show` prints: a
    /// `config` of the NAT64 `prefixes` (each a /96), the public IPv4 `pool`,
    /// session `timeout`, `tcp_established_timeout` and
    /// `tcp_transitory_timeout`, the `mtu` limits, the translated source-port
    /// `port_range` and the `map` session-table
    /// link. It replaces the whole config: what it leaves out is removed,
    /// an absent field taking its default. The `name` may be omitted, it is
    /// then taken from `--name`, and a file naming another config is
    /// refused.
    #[arg(value_name = "PATH")]
    pub file: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteConfigCmd {
    /// The name of the config to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();

    // The update file is read and bound before the connection, so bad
    // local input fails deterministically with or without a reachable
    // gateway.
    let update = match &cmd.mode {
        ModeCmd::Update(update) => {
            let endpoint = client::resolve_label(&cmd.globals.connection, action)?;
            let mut request: UpdateConfigRequest = yaml::load_document(&update.file)
                .map_err(|err| Error::invalid_argument("update", endpoint.clone(), err.to_string()))?;
            yaml::bind_name(&mut request.name, &update.config_name)
                .map_err(|err| Error::invalid_argument("update", endpoint, err))?;
            Some(request)
        }
        _ => None,
    };

    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::List => list_configs(&mut service).await,
        ModeCmd::Show(cmd) => show_config(&mut service, cmd).await,
        ModeCmd::Update(cmd) => {
            let request = update.expect("prepared for the update mode");
            update_config(&mut service, cmd, request).await
        }
        ModeCmd::Delete(cmd) => delete_config(&mut service, cmd).await,
        ModeCmd::Sessions(cmd) => show_sessions(&mut service, cmd).await,
    }
}

type NAT64sService = Service<Nat64sServiceClient<LayeredChannel>>;

async fn list_configs(service: &mut NAT64sService) -> Result<(), Error> {
    let response = service
        .unary("list", ListConfigsRequest {}, async |client, request| {
            client.list_configs(request).await
        })
        .await?;

    output::data(
        || &response.configs,
        || {
            display::print_names_with_hint(
                &response.configs,
                format_args!("No stateful NAT64 configurations found."),
                format_args!("create one with 'yanet-cli-nat64s update --name <name> <path>'"),
            )
        },
    );

    Ok(())
}

async fn show_config(service: &mut NAT64sService, cmd: ShowConfigCmd) -> Result<(), Error> {
    let request = ShowConfigRequest { name: cmd.config_name.clone() };
    let response = service
        .unary_with(
            "show",
            request,
            service.not_found("show", &format!("config '{}'", cmd.config_name)),
            async |client, request| client.show_config(request).await,
        )
        .await?;

    output::data(
        || &response,
        || {
            // The document is printed even with an empty pool, so a
            // redirected show yields a file update accepts.
            print!(
                "{}",
                serde_yaml::to_string(&response).expect("nat64s config YAML serialization must not fail")
            );

            if response.config.as_ref().is_none_or(|config| config.pool.is_empty()) {
                output::empty_with_hint(
                    format_args!("No pool found for '{}'.", cmd.config_name),
                    format_args!("create one with 'yanet-cli-nat64s update --name <name> <path>'"),
                );
            }
        },
    );

    Ok(())
}

async fn update_config(
    service: &mut NAT64sService,
    cmd: UpdateConfigCmd,
    request: UpdateConfigRequest,
) -> Result<(), Error> {
    service
        .unary("update", request, async |client, request| {
            client.update_config(request).await
        })
        .await?;

    output::success("update", format_args!("Updated config '{}'.", cmd.config_name));

    Ok(())
}

async fn delete_config(service: &mut NAT64sService, cmd: DeleteConfigCmd) -> Result<(), Error> {
    let request = DeleteConfigRequest { name: cmd.config_name.clone() };
    service
        .unary_with(
            "delete",
            request,
            service.not_found("delete", &format!("config '{}'", cmd.config_name)),
            async |client, request| client.delete_config(request).await,
        )
        .await?;

    output::success("delete", format_args!("Deleted config '{}'.", cmd.config_name));

    Ok(())
}

/// The column layout of the human-readable session table: the client,
/// the remote it talks to and the external identity, each as an address
/// and a right-aligned port, then the state and two durations. The
/// header and every row are laid out from this one format, so the names
/// cannot drift off their columns.
macro_rules! session_columns {
    ($client:expr, $client_port:expr, $remote:expr, $remote_port:expr, $external:expr, $external_port:expr, $state:expr, $idle:expr, $remaining:expr $(,)?) => {
        format!(
            "{:<39} {:>5}  {:<39} {:>5}  {:<15} {:>5}  {:<12} {:>7}  {:>7}",
            $client, $client_port, $remote, $remote_port, $external, $external_port, $state, $idle, $remaining,
        )
    };
}

/// The names of the human-readable session table's columns, laid out
/// with [`session_columns`].
fn sessions_header() -> String {
    session_columns!(
        "CLIENT", "PORT", "REMOTE", "PORT", "EXTERNAL", "PORT", "STATE", "IDLE", "EXPIRES"
    )
}

/// Where a TCP session stands, for the human-readable table.
///
/// Both ends closing is the transitory state; one end closing is a
/// half-close, named after the end that closed; a session the remote has
/// answered, without a close, is established; anything else is still
/// opening.
fn session_state(session: &nat64spb::Session) -> &'static str {
    match (session.client_closed, session.remote_closed) {
        (true, true) => "closing",
        (true, false) => "cli-closed",
        (false, true) => "rem-closed",
        _ if session.established => "established",
        _ => "opening",
    }
}

/// A duration of the dataplane's clock, rounded down to whole seconds
/// and spelled without spaces, so it stays one word in a table column.
fn format_seconds(nanos: u64) -> String {
    humantime::format_duration(Duration::from_secs(nanos / 1_000_000_000))
        .to_string()
        .replace(' ', "")
}

/// The text of a session's column, `fallback` when the wire message
/// carries no address.
fn address<T: fmt::Display>(address: &Option<T>, fallback: &str) -> String {
    address
        .as_ref()
        .map(ToString::to_string)
        .unwrap_or_else(|| fallback.to_string())
}

/// One line of the human-readable session table.
///
/// `now` is the instant the session's page was read at. A session's
/// timestamps are on the same clock, so its idle time and remaining
/// lifetime are differences from it. A session the dataplane expires
/// between the read and the print shows no remaining time.
fn session_line(session: &nat64spb::Session, now: u64) -> String {
    session_columns!(
        address(&session.client_addr, "?"),
        session.client_port,
        address(&session.remote_addr, "?"),
        session.remote_port,
        address(&session.external_addr, "?"),
        session.external_port,
        session_state(session),
        format_seconds(now.saturating_sub(session.updated_at)),
        format_seconds(session.expires_at.saturating_sub(now)),
    )
}

/// One row of the session table under `--format json`: the wire session,
/// so a script reads the same fields the control plane holds.
type SessionRow = nat64spb::Session;

async fn show_sessions(service: &mut NAT64sService, cmd: ShowSessionsCmd) -> Result<(), Error> {
    // A rejected request, an unknown config included, fails this call
    // before any page is sent, the same as a unary call.
    let request = ShowSessionsRequest {
        name: cmd.config_name.clone(),
        page_size: cmd.page_size,
    };
    let mut stream = service
        .client()
        .show_sessions(request)
        .await
        .map_err(service.not_found("sessions", &format!("config '{}'", cmd.config_name)))?
        .into_inner();

    // Each page is printed as it arrives rather than collected first: a
    // full table is too large to hold, and the first rows should not
    // wait for the last.
    //
    // The read instant reaches the renderer through a cell: the stream
    // loop and the renderer both hold it by shared reference, since the
    // loop cannot assign a plain variable the renderer borrowed. Every
    // page carries the same instant, so the value is set on each page
    // and only ever read between.
    let now = Cell::new(0);
    let mut rows = output::rows(
        || println!("{}", sessions_header()),
        |session: &SessionRow| {
            println!("{}", session_line(session, now.get()));
        },
    );
    let mut shown: u64 = 0;
    while let Some(page) = stream.message().await.map_err(service.status("sessions"))? {
        now.set(page.now);
        for session in &page.sessions {
            rows.push(session);
            shown += 1;
            if cmd.limit.is_some_and(|limit| shown >= limit) {
                return Ok(());
            }
        }
    }

    rows.finish(format_args!("No sessions found for '{}'.", cmd.config_name));

    Ok(())
}

fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_shown_config_round_trips_into_the_update_request() {
        let shown = nat64spb::ShowConfigResponse {
            config: Some(nat64spb::Config {
                prefixes: vec!["64:ff9b::/96".parse().expect("valid prefix in test fixture")],
                pool: vec!["198.51.100.1".parse().expect("valid IPv4 address in test fixture")],
                timeout: 300,
                tcp_established_timeout: 7440,
                tcp_transitory_timeout: 240,
                mtu: Some(nat64spb::MtuConfig { ipv4_mtu: 1450, ipv6_mtu: 1280 }),
                port_range: Some(nat64spb::PortRange { lo: 1024, hi: 4096 }),
                map: "sessions".into(),
            }),
        };

        let yaml = serde_yaml::to_string(&shown).expect("shown config must serialize");
        let parsed: UpdateConfigRequest = serde_yaml::from_str(&yaml).expect("shown config must parse back");

        assert_eq!(shown.config, parsed.config);
    }

    #[test]
    fn test_file_rejects_an_unknown_config_key() {
        let yaml = "name: t0\nconfig:\n  future: true\n";

        let parsed: Result<UpdateConfigRequest, _> = serde_yaml::from_str(yaml);

        assert!(parsed.is_err());
    }

    /// A session with every optional field present, as a control plane
    /// that read it off a live table sends it.
    fn session() -> nat64spb::Session {
        nat64spb::Session {
            proto: 6,
            client_addr: Some(
                "2001:db8::1"
                    .parse::<core::net::IpAddr>()
                    .expect("valid client address")
                    .into(),
            ),
            client_port: 50000,
            remote_addr: Some(
                "64:ff9b::c000:222"
                    .parse::<core::net::IpAddr>()
                    .expect("valid remote address")
                    .into(),
            ),
            remote_port: 443,
            external_addr: Some(commonpb::pb::IPv4Address { addr: 0xc6336401 }),
            external_port: 2000,
            established: true,
            client_closed: false,
            remote_closed: false,
            created_at: 1_000_000_000,
            updated_at: 61_000_000_000,
            expires_at: 3_061_000_000_000,
        }
    }

    #[test]
    fn test_session_state_names_the_lifecycle() {
        let mut session = session();
        session.established = false;
        assert_eq!("opening", session_state(&session));

        session.established = true;
        assert_eq!("established", session_state(&session));

        // A half-close outranks established: the state names the end
        // that closed, so an operator sees which half is still open.
        session.client_closed = true;
        assert_eq!("cli-closed", session_state(&session));

        session.client_closed = false;
        session.remote_closed = true;
        assert_eq!("rem-closed", session_state(&session));

        session.client_closed = true;
        assert_eq!("closing", session_state(&session));
    }

    /// The columns of a table line, in order.
    ///
    /// A column holds no space: the durations are spelled without one for
    /// exactly this reason. Alignment itself cannot be read back off the
    /// text — left-aligned names and right-aligned numbers end in
    /// different places by design — so these tests compare what each
    /// column says, and the layout is left to the one format both the
    /// header and the rows are laid out from.
    ///
    /// Takes the line by value so a column can borrow a line built right
    /// in the call.
    fn columns(line: String) -> Vec<String> {
        line.split_whitespace().map(str::to_owned).collect()
    }

    #[test]
    fn test_sessions_header_names_the_columns_a_row_prints() {
        // One header name per column of a row, in the row's order.
        let header = sessions_header();
        let row = session_line(&session(), 0);

        let names = columns(header.clone());
        let values = columns(row.clone());
        assert_eq!(names.len(), values.len(), "{header}\n{row}");
    }

    #[test]
    fn test_session_line_shows_idle_and_remaining_from_the_read_instant() {
        let session = session();
        // The read instant sits 40s after the last refresh, leaving
        // 3000s of the 2h4m established lifetime, 2960s after the idle.
        let now = 61_000_000_000 + 40 * 1_000_000_000;

        let columns = columns(session_line(&session, now));

        let expected = [
            "2001:db8::1",
            "50000",
            "64:ff9b::c000:222",
            "443",
            "198.51.100.1",
            "2000",
            "established",
            "40s",
            "49m20s",
        ]
        .map(str::to_owned);
        assert_eq!(expected.to_vec(), columns);
    }

    #[test]
    fn test_session_line_clamps_an_already_expired_session() {
        let mut session = session();
        // Expiry before the read instant: a session the dataplane
        // expired between reading the slot and printing it.
        session.updated_at = 0;
        session.expires_at = 500_000_000;
        let now = 1_000_000_000;

        let columns = columns(session_line(&session, now));

        // Nothing wraps: idle is the whole span, remaining is zero.
        assert_eq!("1s", columns[columns.len() - 2], "{columns:?}");
        assert_eq!("0s", columns[columns.len() - 1], "{columns:?}");
    }

    #[test]
    fn test_session_line_marks_a_missing_address() {
        let mut session = session();
        session.client_addr = None;
        session.external_addr = None;

        let columns = columns(session_line(&session, 0));

        // A missing address keeps its column and its width's worth of
        // layout: the row still has every column, with a marker where
        // the address would be.
        assert_eq!("?", columns[0], "{columns:?}");
        assert_eq!("?", columns[4], "{columns:?}");
        assert_eq!(9, columns.len(), "{columns:?}");
    }
}
