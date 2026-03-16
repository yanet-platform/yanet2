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

pub use self::interceptor::AuthLayer;

/// Default certificate tag to match in the SSH agent.
///
/// TODO: from config.
const DEFAULT_CERT_TAG: &str = ":insecure:";

/// Supported authentication methods.
#[derive(Debug, Clone, Copy, ValueEnum)]
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
    #[arg(long, default_value = "none", global = true)]
    pub auth: AuthMethod,
}

/// Error type for layer creation.
#[derive(Debug, thiserror::Error)]
pub enum AuthError {
    #[error("SSH agent error: {0}")]
    Agent(#[from] agent::AgentError),
}

/// Create a tower layer based on the CLI auth arguments.
pub async fn create_layer(args: &AuthArgs) -> Result<AuthLayer, AuthError> {
    match args.auth {
        AuthMethod::None => Ok(AuthLayer::nop()),
        AuthMethod::Sshcert => {
            let layer = AuthLayer::from_agent(DEFAULT_CERT_TAG).await?;
            Ok(layer)
        }
    }
}
