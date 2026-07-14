//! `--watch` transition tracking: the merged snapshot diff and the
//! append-only log line renderer.

use std::collections::BTreeMap;

use chrono::Local;
use colored::Colorize;
use readinesspb::pb::{Scope, State};
use ync::{display, output};

use super::layout::{normalize_whitespace, wrap_words};

/// The kind of change observed for one scope in a `--watch` delta message.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Transition {
    /// The scope's state changed away from `previous`.
    StateChanged { previous: State },
    /// The state is unchanged; only the reason changed.
    ReasonChanged,
}

/// Records `scope`'s new data in `snapshot` and classifies the change.
///
/// A scope absent from `snapshot` is treated as having previously been
/// `STATE_UNKNOWN`. `snapshot` is updated with `scope`'s full data so later
/// calls can diff against it.
pub fn record_transition(snapshot: &mut BTreeMap<String, Scope>, scope: &Scope) -> Transition {
    let next = State::try_from(scope.state).unwrap_or_default();
    let previous = snapshot
        .insert(scope.name.clone(), scope.clone())
        .map(|prev| State::try_from(prev.state).unwrap_or_default())
        .unwrap_or(State::Unknown);

    if previous == next {
        Transition::ReasonChanged
    } else {
        Transition::StateChanged { previous }
    }
}

/// Prints one append-only log line for a `--watch` change.
///
/// A state change shows `PREV → NEXT` on the transition line; a reason-only
/// change shows the unchanged state instead. Both kinds then render every
/// `CODE: message` reason (if any) on a wrapped, dim continuation line — the
/// server never resends an unchanged reason, so a persistent failure's
/// message would otherwise only ever be shown once. Long reason text wraps
/// with a hanging indent when stdout is a TTY; a scope with no reasons
/// prints just the transition line.
pub fn print_transition_line(scope: &Scope, name_width: usize, transition: Transition) {
    let colored = output::is_colored();
    let symbols = super::Symbols::new(colored);
    let wrap_width = display::terminal_width();
    let timestamp = Local::now().format("%H:%M:%S");
    let state = State::try_from(scope.state).unwrap_or_default();
    let name = format!("{:<width$}", scope.name, width = name_width);

    let change = match transition {
        Transition::StateChanged { previous } => {
            format!("{} {} {}", super::label(previous), symbols.arrow, super::label(state))
        }
        Transition::ReasonChanged => super::label(state).to_string(),
    };

    let prefix = format!("{timestamp}  {name}  {change}");
    let indent = prefix.chars().count() + 3;

    print!("{prefix}");

    let mut is_first_line = true;
    for reason in &scope.reasons {
        let reason_text = super::format_reason(reason);

        let lines = match wrap_width {
            Some(width) if width > indent => wrap_words(&reason_text, width - indent),
            _ => vec![normalize_whitespace(&reason_text)],
        };

        for line in &lines {
            let styled = if colored {
                line.truecolor(127, 127, 127).to_string()
            } else {
                line.clone()
            };

            if is_first_line {
                print!("   {styled}");
                is_first_line = false;
            } else {
                print!("\n{:indent$}{styled}", "");
            }
        }
    }

    println!();
}

#[cfg(test)]
mod test {
    use super::*;

    fn scope(name: &str, state: State) -> Scope {
        Scope {
            name: name.to_string(),
            state: state as i32,
            reasons: Vec::new(),
            observed_at: None,
            last_transition_time: None,
        }
    }

    #[test]
    fn record_transition_detects_state_change() {
        let mut snapshot = BTreeMap::new();
        snapshot.insert("rib".to_string(), scope("rib", State::Ready));

        let transition = record_transition(&mut snapshot, &scope("rib", State::Degraded));

        assert_eq!(Transition::StateChanged { previous: State::Ready }, transition);
    }

    #[test]
    fn record_transition_detects_reason_only_change() {
        let mut snapshot = BTreeMap::new();
        snapshot.insert("rib".to_string(), scope("rib", State::Degraded));

        let transition = record_transition(&mut snapshot, &scope("rib", State::Degraded));

        assert_eq!(Transition::ReasonChanged, transition);
    }

    #[test]
    fn record_transition_treats_unknown_scope_as_previously_unknown() {
        let mut snapshot = BTreeMap::new();

        let transition = record_transition(&mut snapshot, &scope("neighbours", State::NotReady));

        assert_eq!(Transition::StateChanged { previous: State::Unknown }, transition);
        assert!(snapshot.contains_key("neighbours"));
    }

    #[test]
    fn record_transition_updates_snapshot() {
        let mut snapshot = BTreeMap::new();
        snapshot.insert("rib".to_string(), scope("rib", State::Ready));

        record_transition(&mut snapshot, &scope("rib", State::NotReady));

        assert_eq!(State::NotReady as i32, snapshot["rib"].state);
    }
}
