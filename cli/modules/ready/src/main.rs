//! Generic readiness probe CLI.

use core::fmt::{self, Display, Formatter};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use colored::Colorize;
use prost_types::Timestamp;
use readinesspb::pb::{ReadyRequest, ReadyResponse, Scope, State};
use tabled::{
    Table, Tabled,
    settings::{
        Color, Style,
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
    },
};
use ync::{
    client::ConnectionArgs,
    errors::Error,
    output::{self, CommonFormat},
};

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

/// Run the readiness probe.
async fn run(cmd: Cmd) -> Result<bool, Error> {
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

    let total = response.scopes.len();
    let ready_count = response
        .scopes
        .iter()
        .filter(|scope| scope.state == State::Ready as i32)
        .count();

    output::data(
        &response.scopes,
        response.scopes.is_empty() && missing.is_empty(),
        format_args!("no scopes"),
        || {
            let mut rows: Vec<ReadinessRow> = response.scopes.iter().map(ReadinessRow::from).collect();
            rows.sort_by(|a, b| a.scope.cmp(&b.scope));

            if !rows.is_empty() {
                print_readiness_table(rows);
            }

            if !missing.is_empty() {
                let missing_list = missing.join(", ");
                let label = "missing (not registered):";

                if output::is_colored() {
                    println!("{} {}", label.red(), missing_list.red());
                } else {
                    println!("{label} {missing_list}");
                }
            }

            let missing_count = missing.len();

            if missing_count > 0 {
                println!("summary: {ready_count}/{total} ready, {missing_count} requested scope missing");
            } else {
                println!("summary: {ready_count}/{total} ready");
            }
        },
    );

    Ok(all_ready)
}

/// Wraps a readiness state for colored display in the table.
pub struct StateCell(State);

impl Display for StateCell {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let StateCell(state) = self;
        let name = state.as_str_name().strip_prefix("STATE_").unwrap_or_default();

        if output::is_colored() {
            let colored = match state {
                State::Ready => name.green().to_string(),
                State::Degraded => name.yellow().to_string(),
                State::NotReady => name.red().to_string(),
                State::Unspecified | State::Unknown => name.truecolor(127, 127, 127).to_string(),
            };
            write!(f, "{colored}")
        } else {
            write!(f, "{name}")
        }
    }
}

/// A displayable row for the readiness table.
#[derive(Debug, Tabled)]
pub struct ReadinessRow {
    #[tabled(rename = "Scope")]
    pub scope: String,
    #[tabled(rename = "State")]
    pub state: String,
    #[tabled(rename = "Last Transition")]
    pub last_transition: String,
    #[tabled(rename = "Observed")]
    pub observed: String,
    #[tabled(rename = "Reasons")]
    pub reasons: String,
}

impl From<&Scope> for ReadinessRow {
    fn from(scope: &Scope) -> Self {
        let state = State::try_from(scope.state).unwrap_or_default();
        let state_cell = StateCell(state);

        let reasons = scope
            .reasons
            .iter()
            .map(|reason| format!("{}: {}", reason.code, reason.message))
            .collect::<Vec<_>>()
            .join(", ");

        Self {
            scope: scope.name.clone(),
            state: state_cell.to_string(),
            last_transition: format_age(scope.last_transition_time.as_ref()),
            observed: format_age(scope.observed_at.as_ref()),
            reasons,
        }
    }
}

fn print_readiness_table(rows: Vec<ReadinessRow>) {
    let mut table = Table::new(&rows);
    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_horizontal(),
    );

    if output::is_colored() {
        table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
        table.modify(Rows::first(), Color::BOLD);
    }

    println!("{table}");
}

/// Formats a `Timestamp` as a human-readable relative age.
///
/// Returns `"-"` when `ts` is `None` or the zero sentinel
/// (`seconds == 0 && nanos == 0`). Otherwise formats as `Xs ago`,
/// `XmYs ago`, or `XhYm ago` depending on magnitude.
pub fn format_age(ts: Option<&Timestamp>) -> String {
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
        let ts = Timestamp { seconds: 0, nanos: 0 };
        assert_eq!("-", format_age(Some(&ts)));
    }

    #[test]
    fn format_age_seconds() {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap();
        let ts = Timestamp {
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
        let ts = Timestamp {
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
        let ts = Timestamp {
            seconds: now.as_secs() as i64 - (2 * 3600 + 3 * 60),
            nanos: 0,
        };
        assert_eq!("2h3m ago", format_age(Some(&ts)));
    }
}
