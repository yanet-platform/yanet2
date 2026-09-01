//! Authentication support for yanet CLI modules.
//!
//! Provides a pluggable authentication framework.
//!
//! Currently supports:
//! - `none` — No authentication.
//! - `sshcert` — SSH certificate authentication via `ssh-agent`.
//!
//! Prefer using [`crate::client::connect`] which wires auth automatically.
//! See the [`crate::client`] module for details.

pub mod agent;
pub mod interceptor;
pub mod token;

use clap::ValueEnum;
use serde::Deserialize;

pub use self::interceptor::AuthLayer;

/// Supported authentication methods.
#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum AuthMethod {
    None,
    /// SSH certificate authentication via ssh-agent.
    Sshcert,
}

/// CLI arguments for authentication.
///
/// Embed this in your module's `Cmd` struct with `#[command(flatten)]`.
#[derive(Debug, Clone, clap::Args)]
pub struct AuthArgs {
    /// Authentication method.
    ///
    /// Falls back to the `auth` key of the configuration file, then to
    /// `none`.
    #[arg(long, global = true, env = "YANET_AUTH")]
    pub auth: Option<AuthMethod>,
    /// Substring matched against a certificate's key id to select it from
    /// the SSH agent, required when `--auth sshcert` is in effect.
    ///
    /// Falls back to the `cert_tag` key of the configuration file.
    #[arg(long, global = true, env = "YANET_CERT_TAG")]
    pub cert_tag: Option<String>,
}

/// Error type for layer creation.
#[derive(Debug, thiserror::Error)]
pub enum AuthError {
    #[error("SSH agent error: {0}")]
    Agent(#[from] agent::AgentError),
}

/// A method together with the identity it authenticates as.
///
/// Unlike [`AuthMethod`] alone, an `Sshcert` value here always carries its
/// tag. [`crate::config::Settings::resolved_auth`] checks for a missing one
/// at connect time, so [`create_layer`] never sees an `Sshcert` without one.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ResolvedAuth {
    None,
    Sshcert { tag: String },
}

/// Create a tower layer for a resolved authentication method.
pub async fn create_layer(auth: ResolvedAuth) -> Result<AuthLayer, AuthError> {
    match auth {
        ResolvedAuth::None => Ok(AuthLayer::nop()),
        ResolvedAuth::Sshcert { tag } => Ok(AuthLayer::from_agent(&tag).await?),
    }
}
