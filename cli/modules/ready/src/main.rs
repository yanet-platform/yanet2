//! Generic readiness probe CLI.

use std::{collections::BTreeMap, process::ExitCode, time::SystemTime};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use colored::Colorize;
use readinesspb::pb::{ReadyRequest, ReadyResponse, Scope, State};
use serde::Serialize;
use ync::{
    client::{self, Connection, ConnectionArgs},
    discovery::Family,
    errors::Error,
    output::{self, CommonFormat},
};

mod render;
mod watch;

/// Exit code used when the RPC succeeds but not all scopes are `STATE_READY`.
const EXIT_NOT_READY: u8 = 2;

/// The family of readiness services this CLI probes and discovers.
const READINESS: Family = Family::new("ReadinessService", "ready", "readiness");

/// Probes readiness of the gateway services.
///
/// Connects to the gateway and invokes `/<FQN>/Ready` using tonic's low-level
/// dynamic dispatcher with the shared `readinesspb` message types. No
/// per-service generated client is needed. The services to probe are
/// discovered from the gateway registry, so neither the user nor the CLI has
/// to keep a list of them.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
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
    /// With a single service named, streams that one service's transition
    /// log. Otherwise (the default with no service named, or with `--all`)
    /// watches every discovered service at once over one shared connection:
    /// each service gets its own supervisor that reconnects with backoff on
    /// its own, and every transition lands in one interleaved log, each line
    /// naming the service it belongs to.
    #[arg(long, default_value_t = false)]
    pub watch: bool,
    /// Multiple of a scope's nominal observation interval past which the
    /// scope is flagged `stale` in human output.
    ///
    /// Each scope publishes how often its source nominally re-observes it;
    /// the flag scales that per-scope contract into a staleness threshold,
    /// so a 5m-cadence neighbour monitor and a 1s RIB sampler are judged by
    /// their own cadences. Scopes without an observation contract are never
    /// flagged. `0` disables the tag.
    #[arg(long, default_value_t = 3)]
    pub stale_multiple: u32,
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

fn main() -> ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

/// Run the readiness probe, dispatching to aggregate or single-service mode.
///
/// The single-service mode establishes one connection and drives everything it
/// needs over it: resolving the alias, probing the service and enriching the
/// error hint. A failure to establish it is reported as a failure of the verb
/// the user asked for, `ready`, exactly like a failing probe.
async fn run(cmd: Cmd) -> Result<ExitCode, Error> {
    let ready = if cmd.is_aggregate() {
        run_aggregate(cmd).await?
    } else {
        let name = cmd.name.clone().expect("a non-aggregate command names a service");

        READINESS.require_name(&client::resolve_label(&cmd.connection, "ready")?, &name)?;

        let connection = Connection::connect_for(&cmd.connection, "ready").await?;

        let name = READINESS.resolve(&connection, &name).await?;

        run_service(&cmd, &connection, &name).await?
    };

    Ok(if ready {
        ExitCode::SUCCESS
    } else {
        ExitCode::from(EXIT_NOT_READY)
    })
}

/// Probes one service, one-shot or streaming, and suggests the services that
/// do exist when the probe finds none under that name.
async fn run_service(cmd: &Cmd, connection: &Connection, name: &str) -> Result<bool, Error> {
    let result = if cmd.watch {
        run_watch(cmd, connection, name).await.map(|()| true)
    } else {
        run_once(cmd, connection, name).await
    };

    match result {
        Ok(ready) => Ok(ready),
        Err(err) => Err(READINESS.suggest(connection, err).await),
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
        || &response.scopes,
        || {
            if response.scopes.is_empty() && missing.is_empty() {
                output::empty(format_args!("No scopes found for {name}."));
                return;
            }

            let mut scopes = response.scopes.clone();
            scopes.sort_by(|a, b| a.name.cmp(&b.name));

            if !scopes.is_empty() {
                let width = render::name_width(scopes.iter().map(|scope| scope.name.as_str()));
                render::print_status_block(name, &scopes, width, cmd.stale_multiple, SystemTime::now(), false);
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
async fn run_watch(cmd: &Cmd, connection: &Connection, name: &str) -> Result<(), Error> {
    let mut snapshot: BTreeMap<(String, String), Scope> = BTreeMap::new();
    let mut name_width: Option<usize> = None;

    connection
        .invoke_server_stream::<ReadyRequest, ReadyResponse, _>(
            "ready",
            name,
            "Watch",
            ReadyRequest { scopes: cmd.scopes.clone() },
            |resp| {
                output::data(
                    || &resp.scopes,
                    || {
                        if resp.scopes.is_empty() {
                            output::empty(format_args!("No scopes found for {name}."));
                            return;
                        }

                        let mut scopes = resp.scopes.clone();
                        scopes.sort_by(|a, b| a.name.cmp(&b.name));

                        match name_width {
                            None => {
                                let width = render::name_width(scopes.iter().map(|scope| scope.name.as_str()));
                                name_width = Some(width);

                                for scope in &scopes {
                                    snapshot.insert((name.to_owned(), scope.name.clone()), scope.clone());
                                }

                                render::print_status_block(
                                    name,
                                    &scopes,
                                    width,
                                    cmd.stale_multiple,
                                    SystemTime::now(),
                                    true,
                                );
                            }
                            Some(width) => {
                                for scope in &scopes {
                                    let transition = render::record_transition(&mut snapshot, name, scope);

                                    if transition != render::Transition::Unchanged {
                                        render::print_transition_line(
                                            render::ServiceColumn::None,
                                            scope,
                                            width,
                                            transition,
                                        );
                                    }
                                }
                            }
                        }
                    },
                );
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
        return watch::run(&cmd).await;
    }

    let connection = Connection::connect_for(&cmd.connection, "ready").await?;
    let services = READINESS.list(&connection).await?;

    let mut reports = Vec::with_capacity(services.len());
    for service in services {
        reports.push(probe(&connection, service).await);
    }

    let all_ready = all_ready(&reports);

    output::data(
        || &reports,
        || {
            if reports.is_empty() {
                output::empty(format_args!("No readiness services registered."));
                return;
            }

            let scopes = reports.iter().flat_map(|report| report.scopes.iter());
            let width = render::name_width(scopes.map(|scope| scope.name.as_str()));
            // One clock reading for every block, so staleness tags across
            // services are computed against the same instant.
            let now = SystemTime::now();

            for (idx, report) in reports.iter().enumerate() {
                if idx > 0 {
                    println!();
                }

                print_report(report, width, cmd.stale_multiple, now);
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
fn print_report(report: &ServiceReport, name_width: usize, stale_multiple: u32, now: SystemTime) {
    match &report.error {
        Some(message) => print_service_error(&report.service, message),
        None => render::print_status_block(&report.service, &report.scopes, name_width, stale_multiple, now, false),
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

/// Completion candidates for the service positional: the readiness services
/// the gateway currently knows.
fn service_candidates() -> Vec<CompletionCandidate> {
    READINESS.candidates(Cmd::command)
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
            expected_observation_interval: None,
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

    #[test]
    fn test_cmd_stale_multiple_defaults_to_three() {
        let cmd = Cmd::try_parse_from(["yanet-cli-ready"]).expect("default command must parse");

        assert_eq!(3, cmd.stale_multiple);
    }

    #[test]
    fn test_cmd_stale_multiple_accepts_override() {
        let cmd =
            Cmd::try_parse_from(["yanet-cli-ready", "--stale-multiple", "7"]).expect("staleness multiplier must parse");

        assert_eq!(7, cmd.stale_multiple);
    }

    #[test]
    fn test_cmd_stale_multiple_accepts_zero() {
        let cmd = Cmd::try_parse_from(["yanet-cli-ready", "--stale-multiple", "0"])
            .expect("zero staleness multiplier must parse");

        assert_eq!(0, cmd.stale_multiple);
    }
}
