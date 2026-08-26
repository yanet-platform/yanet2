//! Process-global output context and free-function API.
//!
//! Call [`init`] once from `main` after parsing CLI flags. Every helper
//! (`success`, `failure`, `data`) reads the global state set by that
//! call — callers never need to thread an `Output` reference through
//! their signatures.
//!
//! The extension point is the [`Format`] trait: each CLI declares its own
//! enum of supported formats and implements [`Format`] once. [`init`] accepts
//! any `F: Format`. [`CommonFormat`] is a ready-made `{ Human, Json }` enum
//! for CLIs that do not need additional formats.

use core::fmt::Arguments;
use std::{io::IsTerminal, sync::OnceLock};

use colored::Colorize;
use erased_serde::Serialize as ErasedSerialize;
use serde::Serialize;

use crate::{
    display,
    errors::{Error, ErrorKind},
    logging,
};

/// A user-selectable output format that knows how to build its backend.
///
/// Each CLI declares an enum of supported formats and implements this
/// trait once. `init` then accepts any `F: Format`, so the format-to-
/// backend mapping lives in one place.
pub trait Format {
    /// Construct the `Output` backend for this format choice.
    fn build(self) -> Box<dyn Output>;
}

/// Trait implemented by all output backends.
///
/// Backends are constructed by [`Format::build`] and installed by [`init`].
/// The free-function facade ([`success`], [`failure`], [`data`]) then
/// delegates to the installed instance.
pub trait Output: Send + Sync {
    /// Report a successful mutating operation.
    fn success(&self, action: &str, message: Arguments);

    /// Report a CLI error.
    fn failure(&self, err: &Error);

    /// Output result data.
    ///
    /// An implementation must not call both `payload` and `render`. It may
    /// call either one, or neither. Passing both sides as closures rather
    /// than as values, and honouring that exclusivity, is what lets a
    /// caller hand over two shapes and pay only for the one its backend
    /// uses.
    ///
    /// The thunk's return type carries a lifetime, so the payload may
    /// borrow rather than own — `|| &response.services` and a struct of
    /// borrowed slices are both valid shapes.
    fn data<'a>(&self, payload: &dyn Fn() -> Box<dyn ErasedSerialize + 'a>, render: Box<dyn FnOnce() + 'a>);

    /// Returns `true` if this backend serializes rather than renders.
    ///
    /// [`empty`] and [`empty_with_hint`] consult this on the installed
    /// backend to skip their report on a serializing backend, so a caller
    /// outside a `data` render closure — a hand-rolled streaming command —
    /// gets the same suppression a render-closure caller already gets for
    /// free from [`JsonOutput::data`] never running `render`. Defaults to
    /// `false`; [`JsonOutput`] overrides it.
    fn serializes(&self) -> bool {
        false
    }
}

/// Human-readable output backend.
///
/// Colors and Unicode symbols are enabled when stderr is a TTY, `NO_COLOR`
/// is not set, and the locale advertises UTF-8.
pub struct HumanOutput {
    is_colored: bool,
}

impl HumanOutput {
    /// Detect terminal capability from the environment.
    pub fn detect() -> Self {
        Self { is_colored: is_colored() }
    }
}

impl Output for HumanOutput {
    fn success(&self, _action: &str, message: Arguments) {
        if self.is_colored {
            eprintln!("{} {message}", "[✓]".green());
        } else {
            eprintln!("[OK] {message}");
        }
    }

    fn failure(&self, err: &Error) {
        let prefix = if self.is_colored {
            "[✗]".red().to_string()
        } else {
            "[ERR]".to_owned()
        };

        eprintln!("{prefix} {} failed: {}", err.action, err.message);

        if let Some(endpoint) = &err.endpoint {
            eprintln!("    endpoint:  {endpoint}");
        }

        if let Some(service) = &err.service {
            eprintln!("    service:   {service}");
        }

        if let Some(hint) = &err.hint {
            let mut lines = hint.lines();

            if let Some(first) = lines.next() {
                let label = if self.is_colored {
                    "hint:".yellow().to_string()
                } else {
                    "hint:".to_owned()
                };

                eprintln!("    {label}      {first}");

                for continuation in lines {
                    eprintln!("               {continuation}");
                }
            }
        }

        if log::log_enabled!(log::Level::Debug) {
            if let (Some(code), Some(msg)) = (&err.raw_code, &err.raw_message) {
                eprintln!("    debug:     {code}: {msg}");
            }
        }
    }

    fn data<'a>(&self, _payload: &dyn Fn() -> Box<dyn ErasedSerialize + 'a>, render: Box<dyn FnOnce() + 'a>) {
        render();
    }
}

/// JSON output backend.
pub struct JsonOutput;

impl Output for JsonOutput {
    fn success(&self, action: &str, message: Arguments) {
        let message_string = format!("{message}");
        let envelope = SuccessJson {
            ok: true,
            action,
            message: &message_string,
        };
        let json = serde_json::to_string(&envelope).expect("SuccessJson serialization must not fail");

        println!("{json}");
    }

    fn failure(&self, err: &Error) {
        let fallback_code = match err.kind {
            ErrorKind::ServiceUnregistered => "NotFound",
            ErrorKind::NotFound => "NotFound",
            ErrorKind::InvalidArgument => "InvalidArgument",
            ErrorKind::Auth => "Unauthenticated",
            ErrorKind::Unavailable => "Unavailable",
            ErrorKind::Rpc => "Unknown",
            ErrorKind::Connection => "Connection",
        };
        let obj = ErrorJson {
            ok: false,
            action: &err.action,
            error: ErrorDetailJson {
                code: err.raw_code.as_deref().unwrap_or(fallback_code),
                kind: err.kind.as_str(),
                message: &err.message,
                endpoint: err.endpoint.as_deref(),
                service: err.service.as_deref(),
            },
        };

        let json = serde_json::to_string(&obj).expect("ErrorJson serialization must not fail");

        println!("{json}");
    }

    fn data<'a>(&self, payload: &dyn Fn() -> Box<dyn ErasedSerialize + 'a>, _render: Box<dyn FnOnce() + 'a>) {
        let payload = payload();
        let json = serde_json::to_string(&payload).expect("payload serialization must not fail");

        println!("{json}");
    }

    fn serializes(&self) -> bool {
        true
    }
}

/// Common format set (`Human` + `Json`) ready to embed in a `clap::Args`.
///
/// CLIs that need additional formats define their own enum and
/// implement `Format` for it instead.
#[derive(Debug, Clone, Copy, PartialEq, Eq, clap::ValueEnum)]
pub enum CommonFormat {
    Human,
    Json,
}

impl Format for CommonFormat {
    fn build(self) -> Box<dyn Output> {
        match self {
            Self::Human => Box::new(HumanOutput::detect()),
            Self::Json => Box::new(JsonOutput),
        }
    }
}

static OUTPUT: OnceLock<Box<dyn Output>> = OnceLock::new();

/// Initialise the logger and output backend from a `Format` choice.
///
/// Must be called exactly once from `main` before any `output::*` helper.
/// Panics if called twice or if the logger fails to install.
pub fn init<F: Format>(verbosity: u8, format: F) {
    logging::init(verbosity as usize).expect("logger init failed");
    OUTPUT.set(format.build()).ok().expect("output already initialised");
}

fn current() -> &'static dyn Output {
    &**OUTPUT
        .get()
        .expect("output not initialised — call `output::init` first")
}

/// Report a successful mutating operation.
///
/// Delegates to the installed backend's [`Output::success`]; the channel
/// and shape are backend-specific.
pub fn success(action: &str, message: Arguments) {
    current().success(action, message);
}

/// Report a CLI error.
///
/// Delegates to the installed backend's [`Output::failure`]; the channel
/// and shape are backend-specific.
pub fn failure(err: &Error) {
    current().failure(err);
}

/// Output result data.
///
/// Delegates to the installed backend's [`Output::data`]. `make_payload`
/// runs only if the backend serializes, and `render` runs only if the
/// backend produces free-form output.
///
/// [`empty`] and [`empty_with_hint`] report an empty result and are
/// ordinary functions, callable from anywhere — including a hand-rolled
/// streaming command with no `render` closure at all. They consult
/// [`Output::serializes`] on the installed backend themselves before
/// printing, so a serializing backend never sees their report regardless of
/// whether the caller sits inside a `data` render closure.
pub fn data<P, MakePayload, Render>(make_payload: MakePayload, render: Render)
where
    MakePayload: Fn() -> P,
    P: Serialize,
    Render: FnOnce(),
{
    let payload = move || -> Box<dyn ErasedSerialize + '_> { Box::new(make_payload()) };

    current().data(&payload, Box::new(render));
}

/// Reports an empty result.
///
/// Suppressed when the installed backend serializes — see
/// [`Output::serializes`] for why that check lives here rather than at
/// each call site. Otherwise printed only when stdout is a terminal: a
/// redirected or piped stdout signals a script rather than a human reading
/// along, and a script gets nothing on either channel, matching the
/// precedent set by `gh`.
///
/// Writes to stderr rather than stdout, matching the
/// [`success`](Output::success)/[`failure`](Output::failure) implementations
/// on [`HumanOutput`] — the only backend `empty` ever prints under: stdout
/// stays reserved for the run's actual rows, so `cmd 2>/dev/null` silences
/// this note along with every other status line instead of leaving a
/// non-data line on stdout.
///
/// The whole line — mark and message alike — is greyed via [`dim`], unlike
/// [`HumanOutput`]'s [`success`](Output::success)/[`failure`](Output::failure),
/// which colour only the mark or the `hint:` label: here the whole line is
/// a note about the absence of payload, not the payload itself.
pub fn empty(message: Arguments) {
    if suppress_empty(current().serializes(), stdout_is_terminal()) {
        return;
    }

    print_empty_line(mark_prefix(), &message.to_string());
}

/// Reports an empty result together with the command that would create one.
///
/// See [`empty`] for the suppression rules and colour rules. The hint line
/// is indented four spaces and prefixed `hint:`, matching [`HumanOutput`]'s
/// [`failure`](Output::failure) detail-line indent (`    endpoint:` /
/// `    service:` / `    hint:`), but stays grey with the rest of the report
/// rather than picking up its yellow `hint:` label.
pub fn empty_with_hint(message: Arguments, hint: Arguments) {
    if suppress_empty(current().serializes(), stdout_is_terminal()) {
        return;
    }

    print_empty_line(mark_prefix(), &message.to_string());
    print_empty_line(HINT_PREFIX, &hint.to_string());
}

/// Prefix of the hint line: the four-space detail indent and the `hint:`
/// label.
const HINT_PREFIX: &str = "    hint: ";

/// Returns the mark prefixing an empty-result line.
///
/// The Unicode en dash when colour is enabled, an ASCII hyphen otherwise —
/// a third mark joining the `[✓]`/`[OK]` and `[✗]`/`[ERR]` fallback pairs
/// [`HumanOutput`]'s [`success`](Output::success)/[`failure`](Output::failure)
/// already use.
fn mark_prefix() -> &'static str {
    if is_colored() {
        "[–] "
    } else {
        "[-] "
    }
}

/// Returns `true` if an empty-result report should be suppressed.
///
/// `serializes` is the installed backend's [`Output::serializes`] and
/// `stdout_is_terminal` is the caller's [`stdout_is_terminal`] reading. Both
/// are backed by a `OnceLock` — the installed `OUTPUT` and the memoized TTY
/// check — so each is fixed for the life of a `cargo test` binary and can't
/// be flipped per test case. Taking them as plain `bool`s instead lifts the
/// suppression rule out as a pure predicate, letting every combination below
/// be exercised directly with no real terminal or installed backend needed.
fn suppress_empty(serializes: bool, stdout_is_terminal: bool) -> bool {
    serializes || !stdout_is_terminal
}

/// Writes one line of an empty-result report to stderr, wrapped to the
/// current stderr width with a hanging indent, and greyed via [`dim`] when
/// colour is enabled.
///
/// `prefix` is the literal lead-in before `text` — [`mark_prefix`] for the
/// mark line, [`HINT_PREFIX`] for the hint line — and its character count
/// becomes the indent for any wrapped continuation line, so a continuation
/// lines up under the first line's own text rather than under the prefix.
///
/// `text` is wrapped in its plain, undimmed form and each resulting line is
/// dimmed afterwards: [`dim`] inserts ANSI escape bytes that must never be
/// counted as display columns.
///
/// When the stderr width is unknown, or too narrow to leave room for the
/// indent, `text` is printed on one unwrapped line, exactly as before this
/// function wrapped at all.
fn print_empty_line(prefix: &str, text: &str) {
    let indent = prefix.chars().count();

    let mut lines = match display::stderr_width() {
        Some(width) if width > indent => display::wrap_words(text, width - indent),
        _ => vec![text.to_string()],
    }
    .into_iter();

    let first = lines.next().expect("wrap_words returns at least one line");
    eprintln!("{}", dim(&format!("{prefix}{first}")));

    let indent = " ".repeat(indent);
    for line in lines {
        eprintln!("{}", dim(&format!("{indent}{line}")));
    }
}

/// Returns `true` if stdout is a terminal, memoized on first call.
///
/// This is the "may I print a line that is not data?" gate for a
/// human-format renderer — the print gate for [`empty`] and
/// [`empty_with_hint`], and for any other renderer deciding whether a
/// reassurance line belongs alongside its data. A caller outside a
/// [`data`] render closure must also consult [`Output::serializes`], as
/// [`empty`] does. It is deliberately independent of [`is_colored`], which
/// reads *stderr* and also folds in `NO_COLOR` — reusing it here would make
/// `NO_COLOR=1` suppress the message outright instead of just its colour.
/// It also answers a different question than
/// [`display::terminal_width`]/[`display::stderr_width`], which assume
/// output is wanted and only report how wide it may be.
pub fn stdout_is_terminal() -> bool {
    static STDOUT_TTY: OnceLock<bool> = OnceLock::new();
    *STDOUT_TTY.get_or_init(|| std::io::stdout().is_terminal())
}

/// Returns `true` if ANSI color and Unicode prefixes should be emitted.
///
/// Detected once from the environment on first call: `NO_COLOR` unset,
/// stderr is a TTY, and the locale advertises UTF-8.
pub fn is_colored() -> bool {
    static COLORED: OnceLock<bool> = OnceLock::new();
    *COLORED.get_or_init(|| {
        let no_color = std::env::var_os("NO_COLOR").is_some();
        let is_tty = std::io::stderr().is_terminal();
        !no_color && is_tty && is_utf8_locale()
    })
}

/// Returns `text` greyed for secondary emphasis when colored output is on.
///
/// Uses the same grey as the CLI's other secondary text and degrades to the
/// plain string when `is_colored` is false, so piped or non-TTY output
/// carries no escape codes.
pub fn dim(text: &str) -> String {
    if is_colored() {
        paint_dim(text)
    } else {
        text.to_string()
    }
}

/// Paints `text` in the secondary grey, without consulting [`is_colored`].
///
/// Call this from a site that has already made its own colour decision,
/// either from an explicit `colored` flag or from an enclosing
/// [`is_colored`] branch. Everyone else should call [`dim`], which makes
/// that decision itself.
pub fn paint_dim(text: &str) -> String {
    text.truecolor(127, 127, 127).to_string()
}

/// Returns `true` if the current locale advertises UTF-8 encoding.
fn is_utf8_locale() -> bool {
    for var in ["LC_ALL", "LC_CTYPE", "LANG"] {
        if let Ok(val) = std::env::var(var) {
            let upper = val.to_uppercase();

            if upper.contains("UTF-8") || upper.contains("UTF8") {
                return true;
            }
        }
    }

    false
}

#[derive(Serialize)]
struct SuccessJson<'a> {
    ok: bool,
    action: &'a str,
    message: &'a str,
}

#[derive(Serialize)]
struct ErrorJson<'a> {
    ok: bool,
    action: &'a str,
    error: ErrorDetailJson<'a>,
}

#[derive(Serialize)]
struct ErrorDetailJson<'a> {
    code: &'a str,
    kind: &'a str,
    message: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    endpoint: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    service: Option<&'a str>,
}

#[cfg(test)]
mod test {
    use super::*;

    /// Set on the re-invoked child process to select the direct-call branch
    /// below, instead of re-spawning itself again.
    const REEXEC_ENV: &str = "YANET_CLI_OUTPUT_EMPTY_REEXEC";

    /// `empty`/`empty_with_hint` write nothing to either channel when
    /// stdout is not a terminal.
    ///
    /// `cargo test` intercepts `print!`/`eprint!` above the OS file
    /// descriptor, so a plain in-process call proves nothing about the
    /// real bytes. This re-invokes the test binary as a child process: with
    /// `--nocapture` the child's real stdout/stderr reach the pipes
    /// `Command::output` captures, and those pipes make stdout a non-TTY by
    /// construction, exercising exactly the gate under test. The child's
    /// stdout still carries the test harness's own "running 1 test" /
    /// "test result: …" banner, unrelated to the function under test, so
    /// stderr — which the harness never writes to on a passing run — is
    /// the channel that proves the gate; stdout is checked only for the
    /// absence of the message text itself.
    ///
    /// The `--exact` filter is a literal copy of this test's own path, so a
    /// rename of the function or of `mod test` would make it match nothing
    /// and the child would exit successfully having run zero tests — every
    /// assertion below stays green regardless of the gate. The "running 1
    /// test" check is the positive control that catches that: it fails
    /// unless the filter actually selected this one test.
    #[test]
    fn empty_writes_nothing_when_stdout_is_not_a_tty() {
        const MESSAGE: &str = "no widgets found";
        const HINT: &str = "create one with 'widget create'";

        if std::env::var_os(REEXEC_ENV).is_some() {
            init(0, CommonFormat::Human);
            empty(format_args!("{MESSAGE}"));
            empty_with_hint(format_args!("{MESSAGE}"), format_args!("{HINT}"));
            return;
        }

        let exe = std::env::current_exe().expect("test binary path");
        let output = std::process::Command::new(exe)
            .args([
                "--exact",
                "output::test::empty_writes_nothing_when_stdout_is_not_a_tty",
                "--nocapture",
            ])
            .env(REEXEC_ENV, "1")
            .output()
            .expect("failed to re-invoke the test binary");

        assert!(output.status.success());
        assert!(output.stderr.is_empty());

        let stdout = String::from_utf8(output.stdout).expect("child stdout must be UTF-8");
        assert!(stdout.contains("running 1 test"));
        assert!(!stdout.contains(MESSAGE));
        assert!(!stdout.contains(HINT));
    }

    #[test]
    fn suppress_empty_is_true_for_a_serializing_backend_even_on_a_terminal() {
        assert!(suppress_empty(true, true));
    }

    #[test]
    fn suppress_empty_is_false_only_for_a_non_serializing_backend_on_a_terminal() {
        assert!(!suppress_empty(false, true));
        assert!(suppress_empty(false, false));
        assert!(suppress_empty(true, false));
    }
}
