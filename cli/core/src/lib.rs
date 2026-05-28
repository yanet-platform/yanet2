use crate::output::Format;

pub mod auth;
pub mod client;
pub mod dispatcher;
pub mod display;
pub mod errors;
pub mod logging;
pub mod output;

/// Initialise the logger and output backend from a `Format` choice.
///
/// Must be called exactly once from `main` before any `output::*` helper.
/// Panics if called twice or if the logger fails to install.
pub fn init<F: Format>(verbosity: u8, format: F) {
    self::output::init(verbosity, format);
}
