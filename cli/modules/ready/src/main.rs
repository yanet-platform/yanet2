//! Generic readiness probe CLI.

use core::time::Duration;
use std::collections::BTreeMap;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::{
    CompleteEnv,
    engine::{ArgValueCandidates, CompletionCandidate},
};
use colored::Colorize;
use readinesspb::pb::{ReadyRequest, ReadyResponse, Scope, State};
use serde::Serialize;
use ync::{
    client::{self, Connection, ConnectionArgs},
    errors::{Error, ErrorKind},
    output::{self, CommonFormat},
};

use self::discovery::Resolution;

mod discovery;
mod render;

/// Exit code used when the RPC succeeds but not all scopes are `STATE_READY`.
const EXIT_NOT_READY: i32 = 2;

/// Caption of the hint that lists every discovered readiness service.
const AVAILABLE_SERVICES: &str = "available readiness services:";

/// Budget for a best-effort gateway lookup: an error hint, a shell completion.
///
/// Such a lookup only enriches something the CLI can do without, so it must
/// never be the thing that hangs: a flag-validation error or a tab press would
/// otherwise stall against a slow or blackholed endpoint until the transport
/// gives up. Exceeding the budget is simply a failed lookup — the hint or the
/// candidate list is dropped and the caller carries on. A probe the user did
/// ask for gets no such budget: there a slow gateway must surface as the error
/// it is.
const DISCOVERY_TIMEOUT: Duration = Duration::from_secs(1);

/// Generic readiness probe — calls `Ready` on any `ReadinessService`.
///
/// Connects to the gateway and invokes `/<FQN>/Ready` using tonic's low-level
/// dynamic dispatcher with the shared `readinesspb` message types. No
/// per-service generated client is needed. The services to probe are
/// discovered from the gateway registry, so neither the user nor the CLI has
/// to keep a list of them.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    /// Readiness service to probe: either a fully-qualified gRPC service name
    /// (e.g. `operators.forward.operatorpb.v1.ReadinessService`) or a short
    /// alias matched against the discovered services (e.g. `forward`).
    ///
    /// Omit it to probe every discovered readiness service.
    #[arg(value_name = "SERVICE", add = ArgValueCandidates::new(service_candidates))]
    pub name: Option<String>,
    /// Restrict output to these scope names; empty means all.
    ///
    /// Only meaningful together with an explicit service.
    pub scopes: Vec<String>,
    /// Probe every readiness service registered with the gateway.
    ///
    /// This is what happens anyway when no service is named; the flag only
    /// says so out loud.
    #[arg(long, default_value_t = false, conflicts_with = "name")]
    pub all: bool,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
    /// Stream readiness changes until interrupted instead of exiting after one
    /// snapshot.
    ///
    /// Requires a single service: several interleaved transition logs would be
    /// unreadable.
    #[arg(long, default_value_t = false)]
    pub watch: bool,
    /// Minimum time since a scope was last observed before flagging it
    /// `stale` in human output.
    ///
    /// Readiness sources heartbeat on their own, differing cadences — e.g.
    /// reconcile actuators every 30s, the bird RIB sampler every 1s, and the
    /// slowest in-tree one, the route operator's neighbour monitor, every
    /// 5m. Since the CLI cannot know a given scope's expected interval, the
    /// default is deliberately generous, roughly 3x the slowest known
    /// heartbeat. Accepts humantime durations (e.g. `30s`, `5m`). `0`
    /// disables the tag.
    #[arg(long, default_value = "15m", value_parser = parse_duration)]
    pub stale_after: Duration,
}

impl Cmd {
    /// Whether every discovered service is to be probed.
    ///
    /// Naming no service means the same thing as `--all`, which is why the two
    /// are mutually exclusive rather than one requiring the other.
    fn is_aggregate(&self) -> bool {
        self.all || self.name.is_none()
    }
}

/// One service's outcome in the aggregate probe.
///
/// `error` is set instead of `scopes` when that one service could not be
/// probed; the run itself still succeeds, so a single dead operator cannot
/// hide the state of the others.
#[derive(Debug, Serialize)]
struct ServiceReport {
    service: String,
    scopes: Vec<Scope>,
    error: Option<String>,
}

/// Parses a CLI duration flag via `humantime`.
fn parse_duration(value: &str) -> Result<Duration, String> {
    humantime::parse_duration(value).map_err(|err| err.to_string())
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);
    colored::control::set_override(output::is_colored());

    match run(cmd).await {
        Ok(true) => {}
        Ok(false) => std::process::exit(EXIT_NOT_READY),
        Err(err) => {
            output::failure(&err);
            std::process::exit(err.exit_code());
        }
    }
}

/// Run the readiness probe, dispatching to aggregate or single-service mode.
///
/// The single-service mode establishes one connection and drives everything it
/// needs over it: resolving the alias, probing the service and enriching the
/// error hint. A failure to establish it is reported as a failure of the verb
/// the user asked for, `ready`, exactly like a failing probe.
async fn run(cmd: Cmd) -> Result<bool, Error> {
    if cmd.is_aggregate() {
        return run_aggregate(cmd).await;
    }

    let name = cmd.name.clone().expect("a non-aggregate command names a service");

    if is_blank(&name) {
        return Err(Error::invalid_argument(
            "ready",
            &cmd.connection.endpoint,
            "service name must not be empty",
        ));
    }

    let connection = Connection::connect_for(&cmd.connection, "ready").await?;

    let name = if name.contains('.') {
        name
    } else {
        resolve_alias(&cmd, &connection, &name).await?
    };

    run_service(&cmd, &connection, &name).await
}

/// Whether a service name is empty or whitespace only.
///
/// An empty name is a substring of every service, so as an alias it matches
/// them all — and with a single readiness service registered it would resolve
/// to that one and hand its readiness to the caller as the exit code of a probe
/// they never asked for. `yanet-cli ready "$SERVICE"` on an unset variable is
/// bad input rather than a registry condition, so it is rejected as such, and
/// before anything is discovered.
fn is_blank(name: &str) -> bool {
    name.trim().is_empty()
}

/// Probes one service, one-shot or streaming, and suggests the services that
/// do exist when the probe finds none under that name.
async fn run_service(cmd: &Cmd, connection: &Connection, name: &str) -> Result<bool, Error> {
    let result = if cmd.watch {
        run_watch(cmd, name).await.map(|()| true)
    } else {
        run_once(cmd, connection, name).await
    };

    match result {
        Ok(ready) => Ok(ready),
        Err(err) => Err(suggest_services(connection, err).await),
    }
}

/// Run a single unary `Ready` call and return whether all scopes are ready.
async fn run_once(cmd: &Cmd, connection: &Connection, name: &str) -> Result<bool, Error> {
    let response: ReadyResponse = connection
        .invoke_unary("ready", name, "Ready", ReadyRequest { scopes: cmd.scopes.clone() })
        .await?;

    let returned_names: std::collections::HashSet<&str> =
        response.scopes.iter().map(|scope| scope.name.as_str()).collect();

    let missing: Vec<&str> = cmd
        .scopes
        .iter()
        .map(String::as_str)
        .filter(|name| !returned_names.contains(name))
        .collect();

    let all_scopes_ready = response.scopes.iter().all(is_ready);

    let all_ready = all_scopes_ready && missing.is_empty();

    output::data(
        &response.scopes,
        response.scopes.is_empty() && missing.is_empty(),
        format_args!("no scopes"),
        || {
            let mut scopes = response.scopes.clone();
            scopes.sort_by(|a, b| a.name.cmp(&b.name));

            if !scopes.is_empty() {
                let width = render::name_width(scopes.iter().map(|scope| scope.name.as_str()));
                render::print_status_block(name, &scopes, width, cmd.stale_after, false);
            }

            print_missing(&missing);
        },
    );

    Ok(all_ready)
}

/// Stream readiness updates via `Watch` until the server closes the connection.
///
/// The first message is a full snapshot of all selected scopes and renders
/// the status block; each subsequent message carries only the scopes that
/// changed and renders one append-only log line per scope. Returns `Ok(())`
/// on clean stream close.
async fn run_watch(cmd: &Cmd, name: &str) -> Result<(), Error> {
    let mut snapshot: BTreeMap<String, Scope> = BTreeMap::new();
    let mut name_width: Option<usize> = None;

    client::invoke_server_stream::<ReadyRequest, ReadyResponse, _>(
        &cmd.connection,
        "ready",
        name,
        "Watch",
        ReadyRequest { scopes: cmd.scopes.clone() },
        |resp| {
            output::data(&resp.scopes, resp.scopes.is_empty(), format_args!("no scopes"), || {
                let mut scopes = resp.scopes.clone();
                scopes.sort_by(|a, b| a.name.cmp(&b.name));

                match name_width {
                    None => {
                        let width = render::name_width(scopes.iter().map(|scope| scope.name.as_str()));
                        name_width = Some(width);

                        for scope in &scopes {
                            snapshot.insert(scope.name.clone(), scope.clone());
                        }

                        render::print_status_block(name, &scopes, width, cmd.stale_after, true);
                    }
                    Some(width) => {
                        for scope in &scopes {
                            let transition = render::record_transition(&mut snapshot, scope);
                            render::print_transition_line(scope, width, transition);
                        }
                    }
                }
            });
        },
    )
    .await
}

/// Probes every discovered readiness service over one shared connection.
///
/// The services are probed sequentially, in the sorted order discovery
/// returns them in, and one failing probe does not abort the rest. The
/// scope-name column is measured across every service at once so it cannot
/// jitter from block to block.
async fn run_aggregate(cmd: Cmd) -> Result<bool, Error> {
    if cmd.watch {
        return Err(watch_requires_service(&cmd).await);
    }

    let connection = Connection::connect_for(&cmd.connection, "ready").await?;
    let services = discovery::list_readiness_services(&connection).await?;

    let mut reports = Vec::with_capacity(services.len());
    for service in services {
        reports.push(probe(&connection, service).await);
    }

    let all_ready = all_ready(&reports);

    output::data(
        &reports,
        reports.is_empty(),
        format_args!("no readiness services registered"),
        || {
            let scopes = reports.iter().flat_map(|report| report.scopes.iter());
            let width = render::name_width(scopes.map(|scope| scope.name.as_str()));

            for (idx, report) in reports.iter().enumerate() {
                if idx > 0 {
                    println!();
                }

                print_report(report, width, cmd.stale_after);
            }
        },
    );

    Ok(all_ready)
}

/// Probes one service's `Ready` over the shared connection.
async fn probe(connection: &Connection, service: String) -> ServiceReport {
    let request = ReadyRequest { scopes: Vec::new() };

    match connection
        .invoke_unary::<_, ReadyResponse>("ready", &service, "Ready", request)
        .await
    {
        Ok(response) => {
            let mut scopes = response.scopes;
            scopes.sort_by(|a, b| a.name.cmp(&b.name));

            ServiceReport { service, scopes, error: None }
        }
        Err(err) => ServiceReport {
            service,
            scopes: Vec::new(),
            error: Some(err.message().to_owned()),
        },
    }
}

/// Reports whether every scope of every probed service is ready.
///
/// A failed probe is never ready — its scopes are unknown, not absent — and
/// neither is an empty set of services: `yanet-cli ready` exiting `0` must
/// mean something was checked and found ready.
fn all_ready(reports: &[ServiceReport]) -> bool {
    !reports.is_empty()
        && reports
            .iter()
            .all(|report| report.error.is_none() && report.scopes.iter().all(is_ready))
}

/// Reports whether `scope` is in `STATE_READY`.
fn is_ready(scope: &Scope) -> bool {
    scope.state == State::Ready as i32
}

/// Renders one service's block of the aggregate probe.
fn print_report(report: &ServiceReport, name_width: usize, stale_after: Duration) {
    match &report.error {
        Some(message) => print_service_error(&report.service, message),
        None => render::print_status_block(&report.service, &report.scopes, name_width, stale_after, false),
    }
}

/// Prints the one-line stand-in for a service whose probe failed.
fn print_service_error(service: &str, message: &str) {
    let line = format!("{service}: {message}");

    if output::is_colored() {
        println!("{}", line.red());
    } else {
        println!("{line}");
    }
}

/// Prints the red `missing (not registered): …` line for requested scopes
/// the server did not return at all.
fn print_missing(missing: &[&str]) {
    if missing.is_empty() {
        return;
    }

    let missing_list = missing.join(", ");
    let label = "missing (not registered):";

    if output::is_colored() {
        println!("{} {}", label.red(), missing_list.red());
    } else {
        println!("{label} {missing_list}");
    }
}

/// Resolves a short alias against the services the gateway knows.
///
/// An alias that matches nothing describes the same operational condition as a
/// fully-qualified name the gateway does not know — that readiness service is
/// not registered, its operator down or not yet up — because the alias is
/// resolved against the live registry. It therefore carries the same kind the
/// gateway's own answer would map to, so that a monitoring script gets one exit
/// code for both spellings of the condition. An ambiguous alias, in contrast,
/// really is bad input.
async fn resolve_alias(cmd: &Cmd, connection: &Connection, alias: &str) -> Result<String, Error> {
    let services = discovery::list_readiness_services(connection).await?;
    let endpoint = &cmd.connection.endpoint;

    match discovery::resolve_alias(alias, &services) {
        Resolution::Resolved(name) => Ok(name),
        Resolution::Ambiguous(candidates) => {
            let message = format!("service name \"{alias}\" is ambiguous");

            Err(Error::invalid_argument("ready", endpoint, message)
                .with_hint(services_hint("matching readiness services:", &candidates)))
        }
        Resolution::Unknown => {
            let message = format!("unknown readiness service \"{alias}\"");

            Err(Error::new(ErrorKind::ServiceUnregistered, "ready", endpoint, message)
                .with_hint(services_hint(AVAILABLE_SERVICES, &services)))
        }
    }
}

/// Builds the error for `--watch` without a single service to watch.
///
/// Discovery is best-effort here: the services it finds become the hint, and a
/// gateway that cannot be reached — or does not answer within the budget
/// `discover` gives it — simply leaves the error hintless. The flag
/// combination is wrong either way, and reporting so must not wait on the
/// network.
async fn watch_requires_service(cmd: &Cmd) -> Error {
    let err = Error::invalid_argument("ready", &cmd.connection.endpoint, "--watch requires a single service");

    match discover(&cmd.connection).await {
        Ok(services) => err.with_hint(services_hint(AVAILABLE_SERVICES, &services)),
        Err(..) => err,
    }
}

/// Adds the discovered services to `err`'s hint when it reads like the named
/// service is simply not registered.
///
/// Best-effort: a failed discovery leaves `err` exactly as it was, so a
/// gateway that is down still surfaces the original probe error rather than a
/// second, more confusing one.
async fn suggest_services(connection: &Connection, err: Error) -> Error {
    if !matches!(err.kind(), ErrorKind::ServiceUnregistered | ErrorKind::NotFound) {
        return err;
    }

    match discovery::list_readiness_services(connection).await {
        Ok(services) => err.with_hint(services_hint(AVAILABLE_SERVICES, &services)),
        Err(..) => err,
    }
}

/// Connects to the gateway afresh and lists the readiness services it knows,
/// within `DISCOVERY_TIMEOUT`.
///
/// This is the best-effort half of discovery, and the only half that has to
/// establish its own connection: every caller reaches an endpoint nothing has
/// spoken to yet, purely to enrich something — a hint, a completion — which is
/// why the budget lives here rather than at the call sites. Discovery the user
/// asked for runs over the `Connection` the probe already established and is
/// deliberately unbounded.
async fn discover(connection: &ConnectionArgs) -> Result<Vec<String>, Error> {
    let lookup = async {
        let connection = Connection::connect(connection).await?;

        discovery::list_readiness_services(&connection).await
    };

    match tokio::time::timeout(DISCOVERY_TIMEOUT, lookup).await {
        Ok(services) => services,
        Err(..) => {
            let budget = humantime::format_duration(DISCOVERY_TIMEOUT);
            let message = format!("gateway did not answer within {budget}");

            Err(Error::new(
                ErrorKind::Unavailable,
                "discover",
                &connection.endpoint,
                message,
            ))
        }
    }
}

/// Formats a hint listing `services` one per line under `caption`.
///
/// `output::failure` aligns every hint line after the first under the first,
/// so the names come out as a column.
fn services_hint(caption: &str, services: &[String]) -> String {
    if services.is_empty() {
        return "no readiness services are registered with the gateway".to_owned();
    }

    let mut hint = String::from(caption);

    for service in services {
        hint.push('\n');
        hint.push_str(service);
    }

    hint
}

/// Completion candidates for the service positional: the readiness services
/// the gateway currently knows.
///
/// Strictly best-effort — a tab-completion must never print an error nor hang
/// — so a gateway that is down, slow or refusing us auth yields no candidates
/// at all, `discover`'s time budget covering the slow case. The endpoint comes
/// from the defaults of the command's own flags, `YANET_ENDPOINT` included,
/// since the completer cannot see what the user has typed so far.
fn service_candidates() -> Vec<CompletionCandidate> {
    let Ok(cmd) = Cmd::try_parse_from([env!("CARGO_BIN_NAME")]) else {
        return Vec::new();
    };

    // The completer is called from within this binary's `#[tokio::main]`
    // runtime, which forbids a nested `block_on`, so the lookup gets a thread
    // and a runtime of its own. A panic in it joins as an error and, like
    // every other failure here, yields no candidates.
    let services = std::thread::spawn(move || {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .ok()?;

        runtime.block_on(discover(&cmd.connection)).ok()
    })
    .join()
    .ok()
    .flatten()
    .unwrap_or_default();

    services.into_iter().map(CompletionCandidate::new).collect()
}

#[cfg(test)]
mod test {
    use super::*;

    fn scope(name: &str, state: State) -> Scope {
        Scope {
            name: name.to_owned(),
            state: state as i32,
            reasons: Vec::new(),
            observed_at: None,
            last_transition_time: None,
        }
    }

    fn report(service: &str, scopes: Vec<Scope>) -> ServiceReport {
        ServiceReport {
            service: service.to_owned(),
            scopes,
            error: None,
        }
    }

    fn failed_report(service: &str) -> ServiceReport {
        ServiceReport {
            service: service.to_owned(),
            scopes: Vec::new(),
            error: Some("unknown service".to_owned()),
        }
    }

    #[test]
    fn all_ready_when_every_scope_of_every_service_is_ready() {
        let reports = vec![
            report("a", vec![scope("rib", State::Ready)]),
            report("b", vec![scope("fib", State::Ready), scope("neighbours", State::Ready)]),
        ];

        assert!(all_ready(&reports));
    }

    #[test]
    fn not_ready_when_one_scope_of_one_service_is_not_ready() {
        let reports = vec![
            report("a", vec![scope("rib", State::Ready)]),
            report("b", vec![scope("fib", State::Degraded)]),
        ];

        assert!(!all_ready(&reports));
    }

    #[test]
    fn not_ready_when_one_service_failed_to_be_probed() {
        let reports = vec![report("a", vec![scope("rib", State::Ready)]), failed_report("b")];

        assert!(!all_ready(&reports));
    }

    #[test]
    fn not_ready_without_any_discovered_service() {
        assert!(!all_ready(&[]));
    }

    #[test]
    fn an_empty_service_name_is_blank() {
        assert!(is_blank(""));
    }

    #[test]
    fn a_whitespace_only_service_name_is_blank() {
        assert!(is_blank("  \t "));
    }

    #[test]
    fn a_named_service_is_not_blank() {
        assert!(!is_blank("route"));
    }

    #[test]
    fn services_hint_lists_one_service_per_line() {
        let services = vec!["a.ReadinessService".to_owned(), "b.ReadinessService".to_owned()];

        assert_eq!(
            "available:\na.ReadinessService\nb.ReadinessService",
            services_hint("available:", &services)
        );
    }

    #[test]
    fn services_hint_states_the_registry_is_empty() {
        assert_eq!(
            "no readiness services are registered with the gateway",
            services_hint("available:", &[])
        );
    }

    #[test]
    fn aggregate_mode_is_the_default() {
        let cmd = Cmd::try_parse_from(["yanet-cli-ready"]).expect("no arguments must parse");

        assert!(cmd.is_aggregate());
    }

    #[test]
    fn naming_a_service_leaves_aggregate_mode() {
        let cmd = Cmd::try_parse_from(["yanet-cli-ready", "route"]).expect("a service name must parse");

        assert!(!cmd.is_aggregate());
    }

    #[test]
    fn all_flag_conflicts_with_a_named_service() {
        assert!(Cmd::try_parse_from(["yanet-cli-ready", "--all", "route"]).is_err());
    }
}
