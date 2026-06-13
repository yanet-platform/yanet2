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

use http::uri::PathAndQuery;
use prost::Message;
use tonic::{
    client::Grpc,
    codec::{CompressionEncoding, ProstCodec},
    transport::Channel,
    Request, Status,
};
use tower::Layer;

use crate::{
    auth::{self, interceptor::AuthService, AuthArgs},
    errors::Error,
};

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
    #[arg(long, default_value = "grpc://[::1]:8080", global = true, env = "YANET_ENDPOINT")]
    pub endpoint: String,
    /// Authentication options.
    #[command(flatten)]
    pub auth: AuthArgs,
}

/// Error type for connection establishment.
#[derive(Debug, thiserror::Error)]
pub enum ConnectionError {
    #[error("{0}")]
    Transport(#[from] tonic::transport::Error),
    #[error("invalid URI: {0}")]
    InvalidUri(#[from] http::uri::InvalidUri),
    #[error("auth error: {0}")]
    Auth(#[from] auth::AuthError),
}

/// Connect to the endpoint with all interceptors pre-applied.
pub async fn connect(args: &ConnectionArgs) -> Result<LayeredChannel, ConnectionError> {
    let channel = Channel::from_shared(args.endpoint.clone())?.connect().await?;
    let auth = auth::create_layer(&args.auth).await?;

    Ok(auth.layer(channel))
}

/// Invoke a unary RPC on an arbitrary gRPC service by its fully-qualified
/// name, without a generated client.
///
/// `action` is the user-facing verb used in error messages (e.g. `"ready"`),
/// `service` is the service FQN, `method` is the wire method name (e.g.
/// `"Ready"`). The request and response are any `prost` messages — the
/// shared codec is built from them.
pub async fn invoke_unary<Req, Resp>(
    connection: &ConnectionArgs,
    action: &str,
    service: &str,
    method: &str,
    request: Req,
) -> Result<Resp, Error>
where
    Req: Message + 'static,
    Resp: Message + Default + 'static,
{
    let endpoint = connection.endpoint.clone();

    let channel = connect(connection)
        .await
        .map_err(|err| Error::from_connection(err, action, endpoint.clone()))?;

    let mut grpc = Grpc::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip);

    let path = PathAndQuery::try_from(format!("/{service}/{method}")).map_err(|err| {
        Error::from_status(
            Status::invalid_argument(err.to_string()),
            action,
            endpoint.clone(),
            service,
        )
    })?;

    grpc.ready()
        .await
        .map_err(|err| Error::from_status(Status::unavailable(err.to_string()), action, endpoint.clone(), service))?;

    let codec: ProstCodec<Req, Resp> = ProstCodec::default();

    grpc.unary(Request::new(request), path, codec)
        .await
        .map(|response| response.into_inner())
        .map_err(|status| Error::from_status(status, action, endpoint, service))
}

/// Invoke a server-streaming RPC on an arbitrary gRPC service by its
/// fully-qualified name, without a generated client.
///
/// `action` is the user-facing verb used in error messages (e.g. `"ready"`),
/// `service` is the service FQN, `method` is the wire method name (e.g.
/// `"Watch"`). Each received message is delivered to `on_message`; the
/// function returns `Ok(())` when the server closes the stream cleanly.
/// `tonic::Streaming` never leaks to the caller — all stream state is
/// contained here.
pub async fn invoke_server_stream<Req, Resp, F>(
    connection: &ConnectionArgs,
    action: &str,
    service: &str,
    method: &str,
    request: Req,
    mut on_message: F,
) -> Result<(), Error>
where
    Req: Message + Send + Sync + 'static,
    Resp: Message + Default + Send + Sync + 'static,
    F: FnMut(Resp),
{
    let endpoint = connection.endpoint.clone();

    let channel = connect(connection)
        .await
        .map_err(|err| Error::from_connection(err, action, endpoint.clone()))?;

    let mut grpc = Grpc::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip);

    let path = PathAndQuery::try_from(format!("/{service}/{method}")).map_err(|err| {
        Error::from_status(
            Status::invalid_argument(err.to_string()),
            action,
            endpoint.clone(),
            service,
        )
    })?;

    grpc.ready()
        .await
        .map_err(|err| Error::from_status(Status::unavailable(err.to_string()), action, endpoint.clone(), service))?;

    let codec: ProstCodec<Req, Resp> = ProstCodec::default();

    let mut stream = grpc
        .server_streaming(Request::new(request), path, codec)
        .await
        .map_err(|status| Error::from_status(status, action, endpoint.clone(), service))?
        .into_inner();

    while let Some(message) = stream
        .message()
        .await
        .map_err(|status| Error::from_status(status, action, endpoint.clone(), service))?
    {
        on_message(message);
    }

    Ok(())
}
