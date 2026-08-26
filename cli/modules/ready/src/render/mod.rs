//! Table-free readiness rendering.
//!
//! The mark and state label are fixed-width compile-time constants; the
//! age and the reason text are not columns, so their variable width never
//! shifts anything to their left. The reason text moves to an indented
//! continuation line instead of sharing a row.

mod age;
mod layout;
mod watch;

use std::time::SystemTime;

use colored::Colorize;
use readinesspb::pb::{Reason, Scope, State};
use ync::{display, humanfmt, output};

use self::{age::is_stale, layout::normalize_whitespace};
pub use self::{
    layout::name_width,
    watch::{
        Membership, ServiceColumn, Transition, print_lifecycle_line, print_lost_line, print_membership_line,
        print_transition_line, record_transition,
    },
};

/// Fixed width of the state label cell (`len("NOT_READY")`).
const STATE_WIDTH: usize = 9;
/// Display width of a colored mark (e.g. `[✓]`) plus its separating space.
const REASON_INDENT_COLORED: usize = 3 + 1;
/// Display width of an ASCII mark (e.g. `[ok]`) plus its separating space.
const REASON_INDENT_ASCII: usize = 4 + 1;
/// Colour marking a stale readiness observation's age tag.
const STALE_TAG_COLOR: (u8, u8, u8) = (180, 140, 0);

/// Leading indent for reason continuation lines.
///
/// Equal to the state mark's display width plus its one separating space, so
/// the indent lands directly under the scope-name column regardless of mode.
fn reason_indent(colored: bool) -> usize {
    if colored {
        REASON_INDENT_COLORED
    } else {
        REASON_INDENT_ASCII
    }
}

/// ASCII/Unicode form for the glyphs that aren't state marks: the watch
/// arrow, the header/summary dash, the summary separator, and the
/// watching-suffix ellipsis.
///
/// Chosen once from the same `colored` flag as [`StateStyle`] so every
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
/// `now` is supplied by the caller so every scope in a snapshot is judged
/// against one point in time. The header line is `{name}{dash}{summary}`;
/// `watching` appends a
/// `watching` suffix to that summary and, once the block is printed, adds
/// one trailing blank line to separate it from the `--watch` transition log
/// that follows.
pub fn print_status_block(
    name: &str,
    scopes: &[Scope],
    name_width: usize,
    stale_multiple: u32,
    now: SystemTime,
    watching: bool,
) {
    let colored = output::is_colored();
    let symbols = Symbols::new(colored);
    let wrap_width = display::terminal_width();

    let mut summary = summary_line(scopes, symbols.separator);
    if watching {
        summary.push_str(symbols.separator);
        summary.push_str("watching");
        summary.push_str(symbols.ellipsis);
    }

    let name = if colored {
        name.bold().to_string()
    } else {
        name.to_string()
    };
    println!("{name}{}{summary}", symbols.dash);

    for scope in scopes {
        print_scope_row(scope, name_width, stale_multiple, now, colored, wrap_width);
    }

    if watching {
        println!();
    }
}

/// Prints the standalone `watching…` line aggregate `--watch` shows once,
/// after every service's status block.
///
/// Separated from the blocks above it and the transition log below it by
/// one blank line on each side. Single-service watch does not use this —
/// it appends its `watching` suffix straight onto the block's own summary
/// line via [`print_status_block`]'s `watching` flag instead.
///
/// This line is not data, it is a reassurance that the process is alive and
/// waiting, so it prints only when stdout is a terminal.
pub fn print_watching_line() {
    if !output::stdout_is_terminal() {
        return;
    }

    let colored = output::is_colored();
    let symbols = Symbols::new(colored);
    let text = format!("watching{}", symbols.ellipsis);

    println!();
    println!("{}", dim(&text, colored));
    println!();
}

/// Builds the green all-ready banner's text.
///
/// Reuses [`StateStyle`] for `State::Ready` rather than a bespoke mark, so
/// this line's glyph and color can never drift from the `READY` marks used
/// everywhere else in the render.
fn all_ready_line(colored: bool) -> String {
    let style = StateStyle::new(State::Ready, colored);

    format!("{} {}", style.styled_mark(), style.paint("all subsystems are ready"))
}

/// Prints the distinct banner aggregate `--watch` shows each time the whole
/// set of watched subsystems crosses into fully ready.
///
/// No surrounding blank lines: at startup [`print_watching_line`] leaves a
/// trailing blank above it whenever it prints at all, and when it does not
/// print, a pipe wants no separator anyway. Mid-stream it sits flush under
/// the transition lines that caused the crossing.
pub fn print_all_ready_line() {
    let colored = output::is_colored();
    println!("{}", all_ready_line(colored));
}

/// Prints one scope's row plus any reason continuation lines.
fn print_scope_row(
    scope: &Scope,
    name_width: usize,
    stale_multiple: u32,
    now: SystemTime,
    colored: bool,
    wrap_width: Option<usize>,
) {
    let state = State::try_from(scope.state).unwrap_or_default();
    let (mark, label_cell) = state_cells(state, colored);
    let name = format!("{:<width$}", scope.name, width = name_width);
    let age = humanfmt::format_age(scope.last_transition_time.as_ref(), now);
    let time = time_cell(age.as_deref());

    let mut line = format!("{mark} {name} {label_cell} {time}");

    if is_stale(
        scope.observed_at.as_ref(),
        scope.expected_observation_interval.as_ref(),
        now,
        stale_multiple,
    ) {
        let stale_age = humanfmt::format_age(scope.observed_at.as_ref(), now).unwrap_or_default();
        let tag = format!("stale {stale_age}");
        let tag = if colored {
            let (r, g, b) = STALE_TAG_COLOR;
            tag.truecolor(r, g, b).to_string()
        } else {
            tag
        };
        line.push_str("   ");
        line.push_str(&tag);
    }

    println!("{line}");
    print_reason_lines(&scope.reasons, colored, wrap_width);
}

/// How one `State` renders: its mark glyph and its color.
///
/// This is the single place the state → (mark, color) map lives. Both the
/// snapshot block and the `--watch` transition log build their cells from
/// here, so a state can never be green in one render and yellow in the
/// other. `colored` is supplied by the caller — the global color gate is
/// read once at the top of a render, never here.
struct StateStyle {
    mark: &'static str,
    color: fn(&str) -> String,
    colored: bool,
}

impl StateStyle {
    fn new(state: State, colored: bool) -> Self {
        let (unicode_mark, ascii_mark, color): (&str, &str, fn(&str) -> String) = match state {
            State::Ready => ("[✓]", "[ok]", |s| s.green().to_string()),
            State::Degraded => ("[~]", "[!!]", |s| s.yellow().to_string()),
            State::NotReady => ("[✗]", "[xx]", |s| s.red().to_string()),
            State::Unknown | State::Unspecified => ("[?]", "[??]", output::paint_dim),
        };

        let mark = if colored { unicode_mark } else { ascii_mark };

        Self { mark, color, colored }
    }

    /// Returns the mark glyph in the state's color.
    fn styled_mark(&self) -> String {
        self.paint(self.mark)
    }

    /// Paints `text` in the state's color, or returns it as-is when the
    /// render is not colored.
    fn paint(&self, text: &str) -> String {
        if self.colored {
            (self.color)(text)
        } else {
            text.to_string()
        }
    }
}

/// Returns the (mark, label) cell text for `state`, colored when `colored`
/// is true.
///
/// Both cells are fixed-width within a given color mode (3 chars / 9+ chars
/// for colored Unicode marks, 4 chars / 9+ chars for the ASCII fallback) so
/// a double-width glyph can never shift a column.
fn state_cells(state: State, colored: bool) -> (String, String) {
    let style = StateStyle::new(state, colored);
    let label_cell = format!("{:<width$}", label(state), width = STATE_WIDTH);

    (style.styled_mark(), style.paint(&label_cell))
}

/// Paints `text` in the grey reserved for secondary text.
///
/// Reason text, the `--watch` timestamp, and the transition arrow all share
/// this one grey, so nothing but the state itself competes for attention on
/// a transition line.
fn dim(text: &str, colored: bool) -> String {
    if colored {
        output::paint_dim(text)
    } else {
        text.to_string()
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
    let reason_indent = reason_indent(colored);
    let indent = " ".repeat(reason_indent);

    for reason in reasons {
        let text = format_reason(reason);

        let lines = match wrap_width {
            Some(width) if width > reason_indent => display::wrap_words(&text, width - reason_indent),
            _ => vec![normalize_whitespace(&text)],
        };

        for line in lines {
            println!("{indent}{}", dim(&line, colored));
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

    #[test]
    fn all_ready_line_uncolored_is_ascii_and_escape_free() {
        let line = all_ready_line(false);

        assert!(line.is_ascii());
        assert!(!line.contains('\x1b'));
    }
}
