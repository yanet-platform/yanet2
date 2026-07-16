//! Shared readiness reporting for operator CLIs.
//!
//! Renders the response of a `ReadinessService::Ready` RPC into a table plus
//! a one-line summary, so each operator CLI only has to make the RPC call and
//! hand the result to `report`.

use core::fmt::{self, Display, Formatter};
use std::{collections::HashSet, time::SystemTime};

use colored::Colorize;
use tabled::{
    settings::{
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
        Color, Style,
    },
    Table, Tabled,
};

use crate::{display, humanfmt, output};

/// Renders a `Ready` RPC response and reports whether everything is ready.
///
/// Prints a readiness table for `scopes`, a `missing (not registered):` line
/// for any `requested` scope name absent from `scopes`, and a summary line,
/// all through `output::data`. Returns `true` only when every scope in
/// `scopes` is `STATE_READY` and none of the `requested` names is missing.
pub fn report(scopes: &[readinesspb::pb::Scope], requested: &[String]) -> bool {
    let missing = missing_scopes(scopes, requested);
    let all_ready = scopes.iter().all(is_ready) && missing.is_empty();

    let total = scopes.len();
    let ready_count = scopes.iter().filter(|scope| is_ready(scope)).count();

    output::data(
        &scopes,
        scopes.is_empty() && missing.is_empty(),
        format_args!("no scopes"),
        || {
            let mut rows: Vec<ReadinessRow> = scopes.iter().map(ReadinessRow::from).collect();
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

    all_ready
}

/// Returns the `requested` scope names absent from `scopes`.
fn missing_scopes<'a>(scopes: &[readinesspb::pb::Scope], requested: &'a [String]) -> Vec<&'a str> {
    let returned_names: HashSet<&str> = scopes.iter().map(|scope| scope.name.as_str()).collect();

    requested
        .iter()
        .map(String::as_str)
        .filter(|name| !returned_names.contains(name))
        .collect()
}

/// Reports whether `scope` is `STATE_READY`.
fn is_ready(scope: &readinesspb::pb::Scope) -> bool {
    scope.state == readinesspb::pb::State::Ready as i32
}

/// Wraps a readiness state for colored display in the table.
struct StateCell(readinesspb::pb::State);

impl Display for StateCell {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let StateCell(state) = self;
        let name = state.as_str_name().strip_prefix("STATE_").unwrap_or_default();

        if output::is_colored() {
            let colored = match state {
                readinesspb::pb::State::Ready => name.green().to_string(),
                readinesspb::pb::State::Degraded => name.yellow().to_string(),
                readinesspb::pb::State::NotReady => name.red().to_string(),
                readinesspb::pb::State::Unspecified | readinesspb::pb::State::Unknown => {
                    name.truecolor(127, 127, 127).to_string()
                }
            };
            write!(f, "{colored}")
        } else {
            write!(f, "{name}")
        }
    }
}

#[derive(Debug, Tabled)]
struct ReadinessRow {
    #[tabled(rename = "Scope")]
    scope: String,
    #[tabled(rename = "State")]
    state: String,
    #[tabled(rename = "Last Transition")]
    last_transition: String,
    #[tabled(rename = "Observed")]
    observed: String,
    #[tabled(rename = "Reasons")]
    reasons: String,
}

impl From<&readinesspb::pb::Scope> for ReadinessRow {
    fn from(scope: &readinesspb::pb::Scope) -> Self {
        let state = readinesspb::pb::State::try_from(scope.state).unwrap_or_default();
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
            last_transition: humanfmt::format_age(scope.last_transition_time.as_ref(), SystemTime::now())
                .unwrap_or_else(|| "-".to_string()),
            observed: humanfmt::format_age(scope.observed_at.as_ref(), SystemTime::now())
                .unwrap_or_else(|| "-".to_string()),
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

    display::fit_terminal_width(&mut table);
    println!("{table}");
}

#[cfg(test)]
mod test {
    use super::*;

    fn scope(name: &str, state: readinesspb::pb::State) -> readinesspb::pb::Scope {
        readinesspb::pb::Scope {
            name: name.to_owned(),
            state: state as i32,
            reasons: Vec::new(),
            observed_at: None,
            last_transition_time: None,
        }
    }

    #[test]
    fn missing_scopes_reports_requested_names_absent_from_scopes() {
        let scopes = vec![scope("rib", readinesspb::pb::State::Ready)];
        let requested = vec!["rib".to_owned(), "fib".to_owned()];

        assert_eq!(vec!["fib"], missing_scopes(&scopes, &requested));
    }

    #[test]
    fn missing_scopes_is_empty_when_every_requested_name_is_returned() {
        let scopes = vec![scope("rib", readinesspb::pb::State::Ready)];
        let requested = vec!["rib".to_owned()];

        assert!(missing_scopes(&scopes, &requested).is_empty());
    }

    #[test]
    fn is_ready_true_only_for_state_ready() {
        assert!(is_ready(&scope("rib", readinesspb::pb::State::Ready)));
        assert!(!is_ready(&scope("rib", readinesspb::pb::State::Degraded)));
        assert!(!is_ready(&scope("rib", readinesspb::pb::State::NotReady)));
    }
}
