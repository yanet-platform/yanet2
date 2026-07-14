//! `--watch` transition tracking: the merged snapshot diff and the
//! append-only log line renderer.

use std::collections::BTreeMap;

use chrono::Local;
use readinesspb::pb::{Scope, State};
use ync::{display, output};

use super::{
    StateStyle, Symbols, dim,
    layout::{normalize_whitespace, wrap_words},
};

/// Gap between the transition line's prefix and the reason text that follows
/// it on the same line.
const REASON_GAP: usize = 3;

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

/// The transition line's prefix in both of its forms.
///
/// `plain` carries no ANSI escapes and is the only form ever measured;
/// `styled` is the only form ever printed. Measuring `styled` would count
/// the escape bytes as columns and blow up the hanging indent.
struct Prefix {
    plain: String,
    styled: String,
}

/// Builds the `HH:MM:SS  ✗ name  PREV → NEXT` prefix of a transition line.
///
/// `name` must already be padded to the scope-name column width. Every part
/// is rendered twice from the same text: once bare for [`Prefix::plain`] and
/// once through [`StateStyle`] for [`Prefix::styled`], so the two can never
/// disagree on the visible width. The mark and both state labels are colored
/// by their own state — the previous label keeps its old state's color — and
/// the timestamp and arrow stay grey.
fn transition_prefix(timestamp: &str, name: &str, state: State, transition: Transition, colored: bool) -> Prefix {
    let symbols = Symbols::new(colored);
    let style = StateStyle::new(state, colored);
    let label = super::label(state);

    let (change, styled_change) = match transition {
        Transition::StateChanged { previous } => {
            let previous_style = StateStyle::new(previous, colored);
            let previous_label = super::label(previous);

            (
                format!("{previous_label} {} {label}", symbols.arrow),
                format!(
                    "{} {} {}",
                    previous_style.paint(previous_label),
                    dim(symbols.arrow, colored),
                    style.paint(label)
                ),
            )
        }
        Transition::ReasonChanged => (label.to_string(), style.paint(label)),
    };

    Prefix {
        plain: format!("{timestamp}  {} {name} {change}", style.mark),
        styled: format!(
            "{}  {} {name} {styled_change}",
            dim(timestamp, colored),
            style.styled_mark()
        ),
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
    let wrap_width = display::terminal_width();
    let timestamp = Local::now().format("%H:%M:%S").to_string();
    let state = State::try_from(scope.state).unwrap_or_default();
    let name = format!("{:<width$}", scope.name, width = name_width);

    let prefix = transition_prefix(&timestamp, &name, state, transition, colored);
    let indent = prefix.plain.chars().count() + REASON_GAP;

    print!("{}", prefix.styled);

    let mut is_first_line = true;
    for reason in &scope.reasons {
        let reason_text = super::format_reason(reason);

        let lines = match wrap_width {
            Some(width) if width > indent => wrap_words(&reason_text, width - indent),
            _ => vec![normalize_whitespace(&reason_text)],
        };

        for line in &lines {
            let styled = dim(line, colored);

            if is_first_line {
                print!("{:width$}{styled}", "", width = REASON_GAP);
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

    /// Drops every ANSI SGR escape (`ESC [ … m`) from `text`, leaving the
    /// characters a terminal would actually show.
    fn strip_ansi(text: &str) -> String {
        let mut visible = String::new();
        let mut chars = text.chars();

        while let Some(c) = chars.next() {
            if c != '\x1b' {
                visible.push(c);
                continue;
            }

            for escaped in chars.by_ref() {
                if escaped == 'm' {
                    break;
                }
            }
        }

        visible
    }

    #[test]
    fn transition_prefix_colors_do_not_change_the_measured_width() {
        colored::control::set_override(true);

        let prefix = transition_prefix(
            "14:32:11",
            "rib         ",
            State::NotReady,
            Transition::StateChanged { previous: State::Ready },
            true,
        );

        assert_eq!(prefix.plain, strip_ansi(&prefix.styled));
        assert!(!prefix.plain.contains('\x1b'));
        assert!(prefix.styled.contains('\x1b'));
    }

    #[test]
    fn transition_prefix_uncolored_is_ascii_and_escape_free() {
        let prefix = transition_prefix(
            "14:32:11",
            "rib         ",
            State::NotReady,
            Transition::StateChanged { previous: State::Ready },
            false,
        );

        assert_eq!(prefix.plain, prefix.styled);
        assert!(prefix.plain.is_ascii());
        assert!(prefix.plain.contains("[xx]"));
        assert!(prefix.plain.contains("->"));
    }

    #[test]
    fn transition_prefix_reason_only_change_keeps_one_label() {
        colored::control::set_override(true);

        let prefix = transition_prefix(
            "14:32:11",
            "rib         ",
            State::Degraded,
            Transition::ReasonChanged,
            true,
        );

        assert_eq!(prefix.plain, strip_ansi(&prefix.styled));
        assert!(prefix.plain.contains(StateStyle::new(State::Degraded, true).mark));
        assert!(prefix.plain.contains("rib"));
        assert!(!prefix.plain.contains(Symbols::new(true).arrow));
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
