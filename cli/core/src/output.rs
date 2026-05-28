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
    /// Backends receive both a structured `payload` (for serialization-
    /// oriented formats) and a `render` callback (for free-form rendering
    /// such as tables or trees). Each backend chooses which signal to
    /// honour and which to ignore.
    ///
    /// `is_empty` and `empty_message` describe the empty-result case:
    /// free-form backends may substitute `empty_message` instead of
    /// invoking `render`; serialization-oriented backends typically
    /// serialize the empty `payload` as-is.
    fn data(
        &self,
        payload: &dyn ErasedSerialize,
        is_empty: bool,
        empty_message: Arguments,
        render: Box<dyn FnOnce() + '_>,
    );
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

    fn data(
        &self,
        _payload: &dyn ErasedSerialize,
        is_empty: bool,
        empty_message: Arguments,
        render: Box<dyn FnOnce() + '_>,
    ) {
        if is_empty {
            eprintln!("{empty_message}");
        } else {
            render();
        }
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

    fn data(
        &self,
        payload: &dyn ErasedSerialize,
        _is_empty: bool,
        _empty_message: Arguments,
        _render: Box<dyn FnOnce() + '_>,
    ) {
        let json = serde_json::to_string(payload).expect("payload serialization must not fail");

        println!("{json}");
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
/// Delegates to the installed backend's [`Output::data`]. The backend
/// chooses whether to serialize `payload` or invoke `render`; when the
/// result is empty (`is_empty == true`), free-form backends may print
/// `empty_message` in place of `render`.
pub fn data<P, F>(payload: &P, is_empty: bool, empty_message: Arguments, render: F)
where
    P: Serialize,
    F: FnOnce(),
{
    current().data(
        payload as &dyn ErasedSerialize,
        is_empty,
        empty_message,
        Box::new(render),
    );
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
