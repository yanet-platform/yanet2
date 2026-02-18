//! Authentication support for yanet CLI modules.
//!
//! Provides a pluggable authentication framework. Currently supports:
//! - `sshcert` — SSH certificate authentication via `ssh-agent`.
//!
//! # Usage
//!
//! 1. Embed [`AuthArgs`] in your CLI `Cmd` struct.
//! 2. Call [`create_layer`] to get a tower layer.
//! 3. Use `channel.with(layer)` or `ServiceBuilder` when creating the gRPC
//!    client.
//!
//! ```ignore
//! use ync::auth::{AuthArgs, create_layer};
//!
//! #[derive(clap::Parser)]
//! struct Cmd {
//!     #[command(flatten)]
//!     auth: AuthArgs,
//!     // ...
//! }
//!
//! let channel = Channel::from_shared(endpoint)?.connect().await?;
//! let layer = create_layer(&cmd.auth)?;
//! let client = MyServiceClient::new(layer.layer(channel));
//! ```

pub mod agent;
pub mod interceptor;
pub mod token;

use clap::ValueEnum;

pub use self::interceptor::AuthLayer;

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
pub fn create_layer(args: &AuthArgs) -> Result<AuthLayer, AuthError> {
    match args.auth {
        AuthMethod::None => Ok(AuthLayer::nop()),
        AuthMethod::Sshcert => {
            let layer = AuthLayer::from_agent()?;
            Ok(layer)
        }
    }
}
