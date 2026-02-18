//! Unified gRPC client initialization.
//!
//! Provides [`ConnectionArgs`] (common CLI flags for endpoint + auth) and a
//! [`connect`] helper that returns a channel with all interceptors
//! pre-applied.
//!
//! # Usage
//!
//! ```ignore
//! use ync::client::{ConnectionArgs, connect};
//!
//! #[derive(clap::Parser)]
//! struct Cmd {
//!     #[command(flatten)]
//!     connection: ConnectionArgs,
//! }
//!
//! let channel = connect(&cmd.connection).await?;
//! let client = MyServiceClient::new(channel)
//!     .send_compressed(CompressionEncoding::Gzip)
//!     .accept_compressed(CompressionEncoding::Gzip);
//! ```

use tonic::transport::Channel;
use tower::Layer;

use crate::auth::{self, interceptor::AuthService, AuthArgs};

/// Channel type with all interceptors applied.
///
/// Use this as the type parameter for tonic-generated clients, e.g.
/// `MyServiceClient<LayeredChannel>`.
pub type LayeredChannel = AuthService<Channel>;

/// Common CLI arguments for gRPC connection.
///
/// Embed this in your module's `Cmd` struct with `#[command(flatten)]`.
#[derive(Debug, Clone, clap::Args)]
pub struct ConnectionArgs {
    /// Gateway endpoint.
    #[arg(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Authentication options.
    #[command(flatten)]
    pub auth: AuthArgs,
}

/// Error type for connection establishment.
#[derive(Debug, thiserror::Error)]
pub enum ConnectionError {
    #[error("transport error: {0}")]
    Transport(#[from] tonic::transport::Error),
    #[error("invalid URI: {0}")]
    InvalidUri(#[from] http::uri::InvalidUri),
    #[error("auth error: {0}")]
    Auth(#[from] auth::AuthError),
}

/// Connect to the endpoint with all interceptors pre-applied.
pub async fn connect(args: &ConnectionArgs) -> Result<LayeredChannel, ConnectionError> {
    let channel = Channel::from_shared(args.endpoint.clone())?.connect().await?;
    let layer = auth::create_layer(&args.auth)?;
    Ok(layer.layer(channel))
}
