//! Table-free readiness rendering.
//!
//! The mark and state label are fixed-width compile-time constants; the
//! age and the reason text are not columns, so their variable width never
//! shifts anything to their left. The reason text moves to an indented
//! continuation line instead of sharing a row.

mod age;
mod layout;
mod watch;

use core::time::Duration;
use std::time::SystemTime;

use colored::Colorize;
use readinesspb::pb::{Reason, Scope, State};
use ync::{display, humanfmt, output};

use self::{
    age::is_stale,
    layout::{normalize_whitespace, wrap_words},
};
pub use self::{
    layout::name_width,
    watch::{print_transition_line, record_transition},
};

/// Fixed width of the state label cell (`len("NOT_READY")`).
const STATE_WIDTH: usize = 9;
/// Leading indent for each scope row.
const ROW_INDENT: usize = 2;
/// Leading indent for reason continuation lines.
const REASON_INDENT: usize = 6;

/// ASCII/Unicode form for the glyphs that aren't state marks: the watch
/// arrow, the header/summary dash, the summary separator, and the
/// watching-suffix ellipsis.
///
/// Chosen once from the same `colored` flag as [`state_cells`] so every
/// symbol in the render degrades to plain ASCII together — no glyph can
/// drift out of sync with the marks in a non-UTF-8 locale or piped run.
struct Symbols {
    arrow: &'static str,
    dash: &'static str,
    separator: &'static str,
    ellipsis: &'static str,
}

impl Symbols {
    fn new(colored: bool) -> Self {
        if colored {
            Self {
                arrow: "→",
                dash: " — ",
                separator: " · ",
                ellipsis: "…",
            }
        } else {
            Self {
                arrow: "->",
                dash: " - ",
                separator: ", ",
                ellipsis: "...",
            }
        }
    }
}

/// Renders the full one-shot status block for `scopes` to stdout.
///
/// `name_width` must be computed once via [`name_width`] and held constant
/// across the render — recomputing it per row would make the column jitter.
/// The header line is `{name}{dash}{summary}`; `watching` appends a
/// `watching` suffix to that summary and, once the block is printed, adds
/// one trailing blank line to separate it from the `--watch` transition log
/// that follows.
pub fn print_status_block(name: &str, scopes: &[Scope], name_width: usize, stale_after: Duration, watching: bool) {
    let now = SystemTime::now();
    let colored = output::is_colored();
    let symbols = Symbols::new(colored);
    let wrap_width = display::terminal_width();

    let mut summary = summary_line(scopes, symbols.separator);
    if watching {
        summary.push_str(symbols.separator);
        summary.push_str("watching");
        summary.push_str(symbols.ellipsis);
    }

    println!("{name}{}{summary}", symbols.dash);

    for scope in scopes {
        print_scope_row(scope, name_width, stale_after, now, colored, wrap_width);
    }

    if watching {
        println!();
    }
}

/// Prints one scope's row plus any reason continuation lines.
fn print_scope_row(
    scope: &Scope,
    name_width: usize,
    stale_after: Duration,
    now: SystemTime,
    colored: bool,
    wrap_width: Option<usize>,
) {
    let state = State::try_from(scope.state).unwrap_or_default();
    let (mark, label_cell) = state_cells(state, colored);
    let name = format!("{:<width$}", scope.name, width = name_width);
    let age = humanfmt::format_age(scope.last_transition_time.as_ref(), now);
    let time = time_cell(age.as_deref());

    let mut line = format!("{:width$}{mark} {name} {label_cell} {time}", "", width = ROW_INDENT);

    if is_stale(
        scope.observed_at.as_ref(),
        scope.last_transition_time.as_ref(),
        now,
        stale_after,
    ) {
        let stale_age = humanfmt::format_age(scope.observed_at.as_ref(), now).unwrap_or_default();
        let tag = format!("stale {stale_age}");
        let tag = if colored {
            tag.truecolor(180, 140, 0).to_string()
        } else {
            tag
        };
        line.push_str("   ");
        line.push_str(&tag);
    }

    println!("{line}");
    print_reason_lines(&scope.reasons, colored, wrap_width);
}

/// Returns the (mark, label) cell text for `state`, colored when `colored`
/// is true.
///
/// Both cells are fixed-width within a given color mode (1 char / 9+ chars
/// for colored Unicode marks, 4 chars / 9+ chars for the ASCII fallback) so
/// a double-width glyph can never shift a column.
fn state_cells(state: State, colored: bool) -> (String, String) {
    let (unicode_mark, ascii_mark, color): (&str, &str, fn(&str) -> String) = match state {
        State::Ready => ("✓", "[ok]", |s| s.green().to_string()),
        State::Degraded => ("~", "[!!]", |s| s.yellow().to_string()),
        State::NotReady => ("✗", "[xx]", |s| s.red().to_string()),
        State::Unknown | State::Unspecified => ("?", "[??]", |s| s.truecolor(127, 127, 127).to_string()),
    };

    let label_cell = format!("{:<width$}", label(state), width = STATE_WIDTH);

    if colored {
        (color(unicode_mark), color(&label_cell))
    } else {
        (ascii_mark.to_string(), label_cell)
    }
}

/// Maps a `State` to its label text, stripped of the `STATE_` prefix.
fn label(state: State) -> &'static str {
    match state {
        State::Ready => "READY",
        State::Degraded => "DEGRADED",
        State::NotReady => "NOT_READY",
        State::Unknown => "UNKNOWN",
        State::Unspecified => "UNSPECIFIED",
    }
}

/// Renders the `for <age>` time cell, or `for never` when `age` is `None`.
fn time_cell(age: Option<&str>) -> String {
    format!("for {}", age.unwrap_or("never"))
}

/// Renders a reason's display text.
///
/// Returns the bare `code` when `message` is empty, otherwise `code:
/// message`. Shared by the snapshot block and the `--watch` transition log
/// so a state change and a reason-only change never disagree on how a
/// reason reads.
fn format_reason(reason: &Reason) -> String {
    if reason.message.is_empty() {
        reason.code.clone()
    } else {
        format!("{}: {}", reason.code, reason.message)
    }
}

/// Prints each reason as one or more indented continuation lines.
///
/// A reason with no message prints its bare code. Long text wraps with a
/// hanging indent when `wrap_width` is `Some` (stdout is a TTY); otherwise
/// it prints as one long, grep-friendly line.
fn print_reason_lines(reasons: &[Reason], colored: bool, wrap_width: Option<usize>) {
    let indent = " ".repeat(REASON_INDENT);

    for reason in reasons {
        let text = format_reason(reason);

        let lines = match wrap_width {
            Some(width) if width > REASON_INDENT => wrap_words(&text, width - REASON_INDENT),
            _ => vec![normalize_whitespace(&text)],
        };

        for line in lines {
            let styled = if colored {
                line.truecolor(127, 127, 127).to_string()
            } else {
                line
            };
            println!("{indent}{styled}");
        }
    }
}

/// Builds the `N/M ready · 1 degraded · 1 not ready` summary line.
///
/// Only non-zero categories beyond `ready` are included. `separator` joins
/// the parts and is chosen by the caller from [`Symbols`], so the whole line
/// degrades to ASCII together with the rest of the render.
fn summary_line(scopes: &[Scope], separator: &str) -> String {
    let total = scopes.len();
    let mut ready = 0usize;
    let mut degraded = 0usize;
    let mut not_ready = 0usize;
    let mut unknown = 0usize;

    for scope in scopes {
        match State::try_from(scope.state).unwrap_or_default() {
            State::Ready => ready += 1,
            State::Degraded => degraded += 1,
            State::NotReady => not_ready += 1,
            State::Unknown | State::Unspecified => unknown += 1,
        }
    }

    let mut parts = vec![format!("{ready}/{total} ready")];

    if degraded > 0 {
        parts.push(format!("{degraded} degraded"));
    }
    if not_ready > 0 {
        parts.push(format!("{not_ready} not ready"));
    }
    if unknown > 0 {
        parts.push(format!("{unknown} unknown"));
    }

    parts.join(separator)
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn symbols_ascii_fallback_has_no_unicode() {
        let symbols = Symbols::new(false);

        assert!(symbols.arrow.is_ascii());
        assert!(symbols.dash.is_ascii());
        assert!(symbols.separator.is_ascii());
        assert!(symbols.ellipsis.is_ascii());
    }
}
