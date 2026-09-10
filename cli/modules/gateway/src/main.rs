//! CLI for the YANET gateway service registry.

use core::{
    fmt::{self, Display, Formatter},
    time::Duration,
};
use std::time::SystemTime;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use prost_types::Timestamp;
use serde::Serialize;
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    completion, discovery, display,
    errors::Error,
    humanfmt,
    output::{self, CommonFormat},
};
use ynpb::pb::{
    BackendKind, ListServicesRequest, ListServicesResponse, RegisteredBackend, gateway_client::GatewayClient,
};

/// The fully-qualified gRPC service name used in error messages.
const GATEWAY_SERVICE: &str = "controlplane.ynpb.v1.Gateway";

/// Cell standing for a value that carries no meaning for the row's kind.
const NOT_APPLICABLE: &str = "\u{2014}";

/// Cell standing for a value the registry did not report.
const UNKNOWN: &str = "-";

/// Age past which a backend that stopped refreshing its registration is
/// reported stale.
///
/// This is the gateway's own default registration lifetime: the age at which
/// it stops counting a registration as current. Dropping the entry happens on
/// a sweep of its own, so a backend can still be listed for a while after
/// crossing the line, and a deployment that keeps stale entries on purpose
/// lists it indefinitely — both are what this column is here to name. A
/// deployment that tunes the lifetime passes its own value.
const DEFAULT_STALE_AFTER: &str = "5m";

/// Inspects the gateway service registry.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all services registered with the gateway.
    List(ListCmd),
}

/// Narrows the registry listing and sets when a heartbeat counts as overdue.
#[derive(Debug, Clone, Parser)]
pub struct ListCmd {
    /// Show only backends hosted this way.
    #[arg(long, value_enum)]
    pub kind: Option<Kind>,
    /// Show only services whose name ends with this segment.
    #[arg(long, add = ArgValueCandidates::new(suffix_candidates))]
    pub suffix: Option<String>,
    /// Age at which a backend that stopped registering counts as stale.
    #[arg(long, value_parser = humantime::parse_duration, default_value = DEFAULT_STALE_AFTER)]
    pub stale_after: Duration,
}

impl ListCmd {
    /// Reports whether a registry entry survives the listing filters.
    fn matches(&self, entry: &RegisteredBackend) -> bool {
        if let Some(kind) = self.kind
            && kind != Kind::of(entry)
        {
            return false;
        }

        if let Some(suffix) = &self.suffix
            && !discovery::has_suffix(service_name(entry), suffix)
        {
            return false;
        }

        true
    }

    /// Reports whether the listing was narrowed at all.
    fn narrowed(&self) -> bool {
        self.kind.is_some() || self.suffix.is_some()
    }
}

/// How a registered backend is hosted, as spelled on the command line and in
/// the table.
#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
pub enum Kind {
    #[value(name = "built-in")]
    Builtin,
    InProcess,
    External,
    Unspecified,
}

impl Kind {
    /// Reads the kind off a registry entry, treating a value this build does
    /// not know as unspecified.
    fn of(entry: &RegisteredBackend) -> Self {
        Self::from(BackendKind::try_from(entry.kind).unwrap_or(BackendKind::Unspecified))
    }

    /// Reports whether an age is worth showing for this kind.
    ///
    /// A backend that never refreshes its registration carries the age of
    /// gateway startup, which would read as a sign of life it is not. An
    /// unclassified backend keeps its age because a gateway too old to
    /// classify its backends leaves every one of them unclassified, and the
    /// age was the only signal that gateway ever offered.
    fn shows_age(self) -> bool {
        matches!(self, Self::External | Self::Unspecified)
    }

    /// Reports whether an age can settle whether this kind's backend is
    /// stale.
    ///
    /// Only a backend running in a process of its own is known to refresh
    /// its registration. An unclassified one may equally be a backend that
    /// never refreshes, whose age would then read as overdue for as long as
    /// the gateway has been up, so its age is shown but never judged.
    fn judges_staleness(self) -> bool {
        matches!(self, Self::External)
    }
}

impl From<BackendKind> for Kind {
    fn from(kind: BackendKind) -> Self {
        match kind {
            BackendKind::Builtin => Self::Builtin,
            BackendKind::InProcess => Self::InProcess,
            BackendKind::External => Self::External,
            BackendKind::Unspecified => Self::Unspecified,
        }
    }
}

impl Display for Kind {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let label = match self {
            Self::Builtin => "built-in",
            Self::InProcess => "in-process",
            Self::External => "external",
            Self::Unspecified => "unspecified",
        };

        f.write_str(label)
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = GatewayService::new(&cmd.connection).await?;

    match &cmd.mode {
        ModeCmd::List(list) => service.list_services(list).await,
    }
}

fn client(channel: LayeredChannel) -> GatewayClient<LayeredChannel> {
    GatewayClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

pub struct GatewayService {
    service: Service<GatewayClient<LayeredChannel>>,
}

impl GatewayService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect_for(connection, "gateway", GATEWAY_SERVICE, client).await?;

        Ok(Self { service })
    }

    pub async fn list_services(&mut self, cmd: &ListCmd) -> Result<(), Error> {
        let response: ListServicesResponse = self
            .service
            .unary("gateway", ListServicesRequest {}, async |client, request| {
                client.list_services(request).await
            })
            .await?;

        let now = SystemTime::now();
        let mut selected: Vec<&RegisteredBackend> =
            response.services.iter().filter(|entry| cmd.matches(entry)).collect();
        selected.sort_by(|left, right| order_key(left).cmp(&order_key(right)));

        let entries: Vec<ServiceEntry> = selected
            .into_iter()
            .map(|entry| ServiceEntry::new(entry, now, cmd.stale_after))
            .collect();

        output::data(
            || &entries,
            || {
                if entries.is_empty() {
                    if cmd.narrowed() && !response.services.is_empty() {
                        output::empty(format_args!("No services matched the filters."));
                    } else {
                        output::empty(format_args!("No services registered."));
                    }
                    return;
                }

                let rows: Vec<ServiceRow> = entries.iter().map(|entry| ServiceRow::new(entry, now)).collect();

                display::print_table_from_entries(&rows);
            },
        );

        Ok(())
    }
}

/// Returns the service name a registry entry was registered under.
fn service_name(entry: &RegisteredBackend) -> &str {
    entry
        .backend
        .as_ref()
        .map(|backend| backend.name.as_str())
        .unwrap_or_default()
}

/// Returns the key the listing is ordered by.
///
/// The gateway keeps its registry unordered, so without a key of its own the
/// same registry renders in a different order on every run. Endpoint and kind
/// join the name only to make the order total.
fn order_key(entry: &RegisteredBackend) -> (&str, &str, i32) {
    let endpoint = entry
        .backend
        .as_ref()
        .map(|backend| backend.endpoint.as_str())
        .unwrap_or_default();

    (service_name(entry), endpoint, entry.kind)
}

/// Reports whether a registration aged past the point where the gateway stops
/// counting it as current, or nothing when the age carries no such verdict.
///
/// A kind whose backends are not known to refresh their registration is never
/// judged, and neither is an entry the registry gave no timestamp for.
fn staleness(kind: Kind, ts: Option<&Timestamp>, now: SystemTime, stale_after: Duration) -> Option<bool> {
    if !kind.judges_staleness() {
        return None;
    }

    humanfmt::age(ts, now).map(|age| age > stale_after)
}

/// A registry entry as this CLI reports it: the record the gateway returned
/// plus the staleness judged from its age.
#[derive(Debug, Serialize)]
pub struct ServiceEntry<'a> {
    #[serde(flatten)]
    pub registered: &'a RegisteredBackend,
    pub stale: Option<bool>,
}

impl<'a> ServiceEntry<'a> {
    pub fn new(registered: &'a RegisteredBackend, now: SystemTime, stale_after: Duration) -> Self {
        let stale = staleness(Kind::of(registered), registered.last_seen_at.as_ref(), now, stale_after);

        Self { registered, stale }
    }
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
    #[tabled(rename = "Stale")]
    pub stale: String,
}

impl ServiceRow {
    pub fn new(entry: &ServiceEntry<'_>, now: SystemTime) -> Self {
        let (name, endpoint) = entry
            .registered
            .backend
            .as_ref()
            .map(|backend| (backend.name.clone(), backend.endpoint.clone()))
            .unwrap_or_default();

        let kind = Kind::of(entry.registered);

        Self {
            name,
            kind: kind.to_string(),
            endpoint,
            last_seen: last_seen_cell(kind, entry.registered.last_seen_at.as_ref(), now),
            stale: stale_cell(kind, entry.stale),
        }
    }
}

/// Returns the last-seen cell value for a row.
///
/// A backend that never refreshes its registration would otherwise show the
/// age of gateway startup as if it were a sign of life, so those rows carry
/// no age at all.
fn last_seen_cell(kind: Kind, ts: Option<&Timestamp>, now: SystemTime) -> String {
    if !kind.shows_age() {
        return NOT_APPLICABLE.to_owned();
    }

    humanfmt::format_age(ts, now).unwrap_or_else(|| UNKNOWN.to_owned())
}

/// Returns the staleness cell value for a row.
fn stale_cell(kind: Kind, stale: Option<bool>) -> String {
    if !kind.judges_staleness() {
        return NOT_APPLICABLE.to_owned();
    }

    match stale {
        Some(true) => "yes".to_owned(),
        Some(false) => "no".to_owned(),
        None => UNKNOWN.to_owned(),
    }
}

/// Completion candidates for a `--suffix` argument: the trailing segments of
/// the service names the gateway currently serves.
///
/// Strictly best-effort — see [`completion::candidates`].
fn suffix_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        let response = client.list_services(ListServicesRequest {}).await?.into_inner();

        let mut suffixes: Vec<String> = response
            .services
            .iter()
            .map(|entry| discovery::suffix_of(service_name(entry)).to_owned())
            .filter(|suffix| !suffix.is_empty())
            .collect();
        suffixes.sort();
        suffixes.dedup();

        Ok(suffixes)
    })
}

#[cfg(test)]
mod test {
    use ynpb::pb::BackendDesc;

    use super::*;

    /// Returns a registry entry of the given kind, last seen `age` seconds
    /// ago relative to `now`.
    fn entry(name: &str, kind: BackendKind, now: SystemTime, age: Option<u64>) -> RegisteredBackend {
        let now_secs = now.duration_since(std::time::UNIX_EPOCH).unwrap().as_secs() as i64;

        RegisteredBackend {
            backend: Some(BackendDesc {
                name: name.to_owned(),
                endpoint: "[::1]:8080".to_owned(),
            }),
            last_seen_at: age.map(|age| Timestamp {
                seconds: now_secs - age as i64,
                nanos: 0,
            }),
            kind: kind as i32,
        }
    }

    /// Returns a listing narrowed by the given filters, judging staleness at
    /// five minutes.
    fn list(kind: Option<Kind>, suffix: Option<&str>) -> ListCmd {
        ListCmd {
            kind,
            suffix: suffix.map(str::to_owned),
            stale_after: Duration::from_secs(300),
        }
    }

    #[test]
    fn test_kind_display_all_variants() {
        assert_eq!("built-in", Kind::Builtin.to_string());
        assert_eq!("in-process", Kind::InProcess.to_string());
        assert_eq!("external", Kind::External.to_string());
        assert_eq!("unspecified", Kind::Unspecified.to_string());
    }

    #[test]
    fn test_kind_of_unknown_wire_value_is_unspecified() {
        let mut backend = entry(
            "controlplane.ynpb.v1.Gateway",
            BackendKind::External,
            SystemTime::now(),
            None,
        );
        backend.kind = 99;

        assert_eq!(Kind::Unspecified, Kind::of(&backend));
    }

    #[test]
    fn test_last_seen_cell_builtin_shows_no_age() {
        let now = SystemTime::now();
        let backend = entry("built-in", BackendKind::Builtin, now, Some(5));

        assert_eq!(
            NOT_APPLICABLE,
            last_seen_cell(Kind::Builtin, backend.last_seen_at.as_ref(), now)
        );
    }

    #[test]
    fn test_last_seen_cell_in_process_shows_no_age() {
        let now = SystemTime::now();
        let backend = entry("in-process", BackendKind::InProcess, now, Some(5));

        assert_eq!(
            NOT_APPLICABLE,
            last_seen_cell(Kind::InProcess, backend.last_seen_at.as_ref(), now)
        );
    }

    #[test]
    fn test_last_seen_cell_external_shows_age() {
        let now = SystemTime::now();
        let backend = entry("external", BackendKind::External, now, Some(5));

        assert_eq!("5s", last_seen_cell(Kind::External, backend.last_seen_at.as_ref(), now));
    }

    #[test]
    fn test_last_seen_cell_unspecified_shows_age() {
        let now = SystemTime::now();
        let backend = entry("legacy", BackendKind::Unspecified, now, Some(5));

        assert_eq!(
            "5s",
            last_seen_cell(Kind::Unspecified, backend.last_seen_at.as_ref(), now)
        );
    }

    #[test]
    fn test_last_seen_cell_without_timestamp_is_unknown() {
        assert_eq!(UNKNOWN, last_seen_cell(Kind::External, None, SystemTime::now()));
    }

    #[test]
    fn test_staleness_external_past_threshold_is_stale() {
        let now = SystemTime::now();
        let backend = entry("external", BackendKind::External, now, Some(301));

        let stale = staleness(
            Kind::External,
            backend.last_seen_at.as_ref(),
            now,
            Duration::from_secs(300),
        );

        assert_eq!(Some(true), stale);
    }

    #[test]
    fn test_staleness_external_within_threshold_is_fresh() {
        let now = SystemTime::now();
        let backend = entry("external", BackendKind::External, now, Some(299));

        let stale = staleness(
            Kind::External,
            backend.last_seen_at.as_ref(),
            now,
            Duration::from_secs(300),
        );

        assert_eq!(Some(false), stale);
    }

    #[test]
    fn test_staleness_unspecified_has_no_verdict() {
        let now = SystemTime::now();
        let backend = entry("legacy", BackendKind::Unspecified, now, Some(3600));

        let stale = staleness(
            Kind::Unspecified,
            backend.last_seen_at.as_ref(),
            now,
            Duration::from_secs(300),
        );

        assert_eq!(None, stale);
    }

    #[test]
    fn test_staleness_builtin_has_no_verdict() {
        let now = SystemTime::now();
        let backend = entry("built-in", BackendKind::Builtin, now, Some(3600));

        let stale = staleness(
            Kind::Builtin,
            backend.last_seen_at.as_ref(),
            now,
            Duration::from_secs(300),
        );

        assert_eq!(None, stale);
    }

    #[test]
    fn test_staleness_without_timestamp_has_no_verdict() {
        let stale = staleness(Kind::External, None, SystemTime::now(), Duration::from_secs(300));

        assert_eq!(None, stale);
    }

    #[test]
    fn test_stale_cell_renders_each_verdict() {
        assert_eq!("yes", stale_cell(Kind::External, Some(true)));
        assert_eq!("no", stale_cell(Kind::External, Some(false)));
        assert_eq!(UNKNOWN, stale_cell(Kind::External, None));
        assert_eq!(NOT_APPLICABLE, stale_cell(Kind::Builtin, None));
        assert_eq!(NOT_APPLICABLE, stale_cell(Kind::Unspecified, None));
    }

    #[test]
    fn test_order_key_sorts_registry_by_name() {
        let now = SystemTime::now();
        let entries = [
            entry("controlplane.ynpb.v1.Gateway", BackendKind::Builtin, now, None),
            entry(
                "operators.route.operatorpb.v1.RouteService",
                BackendKind::External,
                now,
                None,
            ),
            entry("controlplane.ynpb.v1.CounterService", BackendKind::Builtin, now, None),
        ];

        let mut selected: Vec<&RegisteredBackend> = entries.iter().collect();
        selected.sort_by(|left, right| order_key(left).cmp(&order_key(right)));

        let names: Vec<&str> = selected.into_iter().map(service_name).collect();

        assert_eq!(
            vec![
                "controlplane.ynpb.v1.CounterService",
                "controlplane.ynpb.v1.Gateway",
                "operators.route.operatorpb.v1.RouteService",
            ],
            names
        );
    }

    #[test]
    fn test_matches_kind_filter_rejects_other_kinds() {
        let now = SystemTime::now();
        let external = entry(
            "operators.route.operatorpb.v1.RouteService",
            BackendKind::External,
            now,
            None,
        );
        let builtin = entry("controlplane.ynpb.v1.Gateway", BackendKind::Builtin, now, None);

        let cmd = list(Some(Kind::External), None);

        assert!(cmd.matches(&external));
        assert!(!cmd.matches(&builtin));
    }

    #[test]
    fn test_matches_suffix_filter_requires_the_trailing_segment() {
        let now = SystemTime::now();
        let readiness = entry("controlplane.ynpb.v1.ReadinessService", BackendKind::Builtin, now, None);
        let gateway = entry("controlplane.ynpb.v1.Gateway", BackendKind::Builtin, now, None);

        let cmd = list(None, Some("ReadinessService"));

        assert!(cmd.matches(&readiness));
        assert!(!cmd.matches(&gateway));
    }

    #[test]
    fn test_matches_combines_kind_and_suffix() {
        let now = SystemTime::now();
        let external = entry(
            "operators.route.operatorpb.v1.ReadinessService",
            BackendKind::External,
            now,
            None,
        );
        let builtin = entry("controlplane.ynpb.v1.ReadinessService", BackendKind::Builtin, now, None);

        let cmd = list(Some(Kind::External), Some("ReadinessService"));

        assert!(cmd.matches(&external));
        assert!(!cmd.matches(&builtin));
    }
}
