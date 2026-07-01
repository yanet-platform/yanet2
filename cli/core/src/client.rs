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

/// A connected gRPC client bundled with its endpoint and service name.
///
/// Wraps a tonic-generated client together with the endpoint it reached and
/// its fully-qualified service name. This lets a CLI stop threading
/// `endpoint` through its own service struct and stop re-declaring the
/// per-command error-mapping helpers: [`status`](Service::status) and
/// [`invalid`](Service::invalid) live here once.
///
/// It holds a single client, matching the common one-service CLI. A CLI that
/// drives several clients over one shared channel keeps them as separate
/// fields instead.
pub struct Service<C> {
    client: C,
    endpoint: String,
    name: &'static str,
}

impl<C> Service<C> {
    /// Wrap an already-built `client` with its `endpoint` and service `name`.
    ///
    /// Prefer [`Service::connect`], which also establishes the channel. Use
    /// this only when the channel is built elsewhere.
    pub fn new(client: C, endpoint: impl Into<String>, name: &'static str) -> Self {
        Self {
            client,
            endpoint: endpoint.into(),
            name,
        }
    }

    /// Connect to `connection` and build the client via `build`.
    ///
    /// `name` is the fully-qualified gRPC service name embedded in error
    /// messages. `build` receives the layered channel and returns the
    /// tonic-generated client, configuring compression and message sizes.
    pub async fn connect<F>(connection: &ConnectionArgs, name: &'static str, build: F) -> Result<Self, Error>
    where
        F: FnOnce(LayeredChannel) -> C,
    {
        let channel = connect(connection)
            .await
            .map_err(|err| Error::from_connection(err, "connect", &connection.endpoint))?;

        Ok(Self::new(build(channel), connection.endpoint.clone(), name))
    }

    /// Mutable access to the inner client for issuing RPCs.
    pub fn client(&mut self) -> &mut C {
        &mut self.client
    }

    /// The endpoint this client reached.
    ///
    /// Exposed for the few call sites that build errors through a helper
    /// other than [`status`](Service::status) / [`invalid`](Service::invalid)
    /// (for example a [`NotFoundMapper`](crate::errors::NotFoundMapper)).
    pub fn endpoint(&self) -> &str {
        &self.endpoint
    }

    /// A closure mapping a gRPC [`Status`] to a structured [`Error`].
    ///
    /// `action` is the user-facing verb (e.g. `"list"`); pass the returned
    /// closure to `Result::map_err` on an RPC result. The closure owns its
    /// captures, so it never borrows `self`.
    pub fn status(&self, action: &'static str) -> impl FnOnce(Status) -> Error {
        let endpoint = self.endpoint.clone();
        let name = self.name;

        move |status| Error::from_status(status, action, endpoint, name)
    }

    /// Build an invalid-argument [`Error`] for input rejected locally.
    ///
    /// Use when the CLI detects a bad argument before issuing the RPC.
    pub fn invalid(&self, action: &'static str, message: impl Into<String>) -> Error {
        Error::from_status(
            Status::invalid_argument(message.into()),
            action,
            self.endpoint.clone(),
            self.name,
        )
    }
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

#[cfg(test)]
mod test {
    use tonic::Status;

    use super::Service;
    use crate::errors::ErrorKind;

    #[test]
    fn status_maps_grpc_code() {
        let service = Service::new((), "grpc://[::1]:8080", "test.Service");
        let err = (service.status("list"))(Status::not_found("missing"));

        assert_eq!(ErrorKind::NotFound, err.kind);
    }

    #[test]
    fn invalid_builds_invalid_argument() {
        let service = Service::new((), "grpc://[::1]:8080", "test.Service");
        let err = service.invalid("update", "bad input");

        assert_eq!(ErrorKind::InvalidArgument, err.kind);
        assert_eq!("bad input", err.message);
    }

    #[test]
    fn endpoint_returns_configured_value() {
        let service = Service::new((), "grpc://[::1]:8080", "test.Service");

        assert_eq!("grpc://[::1]:8080", service.endpoint());
    }
}
