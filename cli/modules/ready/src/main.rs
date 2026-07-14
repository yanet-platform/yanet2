//! Generic readiness probe CLI.

use core::time::Duration;
use std::collections::BTreeMap;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use colored::Colorize;
use readinesspb::pb::{ReadyRequest, ReadyResponse, Scope, State};
use ync::{
    client::ConnectionArgs,
    errors::Error,
    output::{self, CommonFormat},
};

mod render;

/// Exit code used when the RPC succeeds but not all scopes are `STATE_READY`.
const EXIT_NOT_READY: i32 = 2;

/// Generic readiness probe — calls `Ready` on any `ReadinessService`.
///
/// Connects to the gateway and invokes `/<FQN>/Ready` using tonic's low-level
/// dynamic dispatcher with the shared `readinesspb` message types. No
/// per-service generated client is needed.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    /// Fully-qualified gRPC service name, e.g.
    /// `operators.forward.operatorpb.v1.ReadinessService`.
    pub name: String,
    /// Restrict output to these scope names; empty means all.
    pub scopes: Vec<String>,
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

/// Run the readiness probe, dispatching to one-shot or streaming mode.
async fn run(cmd: Cmd) -> Result<bool, Error> {
    if cmd.watch {
        run_watch(cmd).await.map(|()| true)
    } else {
        run_once(cmd).await
    }
}

/// Run a single unary `Ready` call and return whether all scopes are ready.
async fn run_once(cmd: Cmd) -> Result<bool, Error> {
    let response: ReadyResponse = ync::client::invoke_unary(
        &cmd.connection,
        "ready",
        &cmd.name,
        "Ready",
        ReadyRequest { scopes: cmd.scopes.clone() },
    )
    .await?;

    let returned_names: std::collections::HashSet<&str> =
        response.scopes.iter().map(|scope| scope.name.as_str()).collect();

    let missing: Vec<&str> = cmd
        .scopes
        .iter()
        .map(String::as_str)
        .filter(|name| !returned_names.contains(name))
        .collect();

    let all_scopes_ready = response.scopes.iter().all(|scope| scope.state == State::Ready as i32);

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
                render::print_status_block(&cmd.name, &scopes, width, cmd.stale_after, false);
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
async fn run_watch(cmd: Cmd) -> Result<(), Error> {
    let mut snapshot: BTreeMap<String, Scope> = BTreeMap::new();
    let mut name_width: Option<usize> = None;

    ync::client::invoke_server_stream::<ReadyRequest, ReadyResponse, _>(
        &cmd.connection,
        "ready",
        &cmd.name,
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

                        render::print_status_block(&cmd.name, &scopes, width, cmd.stale_after, true);
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
