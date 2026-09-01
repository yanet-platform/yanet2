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

use core::time::Duration;
use std::{
    fs,
    path::{Path, PathBuf},
};

use http::uri::PathAndQuery;
use prost::Message;
use tonic::{
    client::Grpc,
    codec::{CompressionEncoding, ProstCodec},
    transport::{Certificate, Channel, ClientTlsConfig, Endpoint, Identity},
    Request, Status,
};
use tower::Layer;

use crate::{
    auth::{self, interceptor::AuthService, AuthArgs},
    config::{self, Settings},
    errors::{root_cause, Error},
    timeout::{TimeoutLayer, TimeoutService},
};

/// Channel type with all interceptors applied.
///
/// Use this as the type parameter for tonic-generated clients, e.g.
/// `MyServiceClient<LayeredChannel>`.
pub type LayeredChannel = TimeoutService<AuthService<Channel>>;

/// TLS material supplied explicitly by the user.
#[derive(Debug, Clone, Default, clap::Args)]
pub struct TlsArgs {
    /// PEM CA bundle that replaces native system roots for server verification.
    ///
    /// Native system roots are used when this option is omitted.
    #[arg(long, global = true, env = "YANET_CA", value_name = "PATH")]
    pub ca: Option<PathBuf>,
    /// PEM client certificate chain for mutual TLS.
    ///
    /// The configuration file may supply the matching key.
    #[arg(long, global = true, env = "YANET_CLIENT_CERT", value_name = "PATH")]
    pub client_cert: Option<PathBuf>,
    /// PEM client private key for mutual TLS.
    #[arg(long, global = true, env = "YANET_CLIENT_KEY", value_name = "PATH")]
    pub client_key: Option<PathBuf>,
}

impl Settings {
    fn tls_configured(&self) -> bool {
        self.ca.value.is_some() || self.client_cert.value.is_some() || self.client_key.value.is_some()
    }
}

/// Common CLI arguments for gRPC connection.
///
/// Embed this in your module's `Cmd` struct with `#[command(flatten)]`.
#[derive(Debug, Clone, clap::Args)]
pub struct ConnectionArgs {
    /// Gateway endpoint (grpc://, grpcs://, or unix://) or an alias from the
    /// configuration file.
    ///
    /// Falls back to the `endpoint` key of the configuration file,
    /// /etc/yanet2/cli.yaml merged with $XDG_CONFIG_HOME/yanet2/cli.yaml
    /// (~/.config by default) or the single file named by YANET_CONFIG,
    /// then to grpc://[::1]:8080. A value without :// is an alias from
    /// the file's `endpoints:` map.
    #[arg(long, global = true, env = "YANET_ENDPOINT")]
    pub endpoint: Option<String>,
    /// Authentication options.
    #[command(flatten)]
    pub auth: AuthArgs,
    #[command(flatten)]
    pub tls: TlsArgs,
    /// Time budget in seconds for connecting and for each request.
    ///
    /// Long-lived streams are bounded only while being established. Falls
    /// back to the `timeout` key of the configuration file.
    #[arg(long, global = true, env = "YANET_TIMEOUT", value_name = "SECONDS", value_parser = parse_timeout)]
    pub timeout: Option<Duration>,
}

/// Parses a positive, possibly fractional, number of seconds.
fn parse_timeout(value: &str) -> Result<Duration, String> {
    let seconds: f64 = value
        .parse()
        .map_err(|_| "expected a positive number of seconds".to_owned())?;

    timeout_from_seconds(seconds)
}

/// Converts a positive, possibly fractional, number of seconds into a
/// [`Duration`], the shared rule behind [`parse_timeout`] and the file's
/// `timeout` key.
pub(crate) fn timeout_from_seconds(seconds: f64) -> Result<Duration, String> {
    if !seconds.is_finite() || seconds <= 0.0 {
        return Err("expected a positive number of seconds".to_owned());
    }

    Duration::try_from_secs_f64(seconds).map_err(|err| err.to_string())
}

/// Error type for connection establishment.
#[derive(Debug, thiserror::Error)]
pub enum ConnectionError {
    #[error("{}", root_cause(.0))]
    Transport(#[from] tonic::transport::Error),
    #[error("invalid URI: {0}")]
    InvalidUri(#[from] http::uri::InvalidUri),
    #[error("unsupported endpoint scheme: {0}")]
    InvalidEndpointScheme(String),
    #[error("TLS options require a grpcs:// endpoint")]
    TlsOptionsWithoutTls,
    #[error("client certificate and key must be provided together")]
    IncompleteClientIdentity,
    #[error("failed to read {kind} from {}: {source}", path.display())]
    TlsFile {
        kind: &'static str,
        path: PathBuf,
        #[source]
        source: std::io::Error,
    },
    #[error("invalid TLS configuration: {}", root_cause(.0))]
    TlsConfig(tonic::transport::Error),
    #[error("auth error: {0}")]
    Auth(#[from] auth::AuthError),
    #[error("connect timed out after {}s", .0.as_secs_f64())]
    Timeout(Duration),
    #[error("{0}")]
    Config(#[from] config::Error),
}

/// Rejects TLS material before any plaintext transport can use it.
fn build_endpoint(settings: &Settings) -> Result<Endpoint, ConnectionError> {
    let endpoint = &settings.endpoint.value;

    match endpoint.split_once("://") {
        Some(("grpcs", address)) => {
            let channel = Channel::from_shared(format!("https://{address}"))?;
            let tls = build_tls_config(settings)?;

            channel.tls_config(tls).map_err(ConnectionError::TlsConfig)
        }
        Some(("grpc" | "unix", ..)) if settings.tls_configured() => Err(ConnectionError::TlsOptionsWithoutTls),
        Some(("grpc", ..)) => Channel::from_shared(endpoint.clone()).map_err(ConnectionError::InvalidUri),
        Some(("unix", ..)) => Endpoint::from_shared(endpoint.clone()).map_err(ConnectionError::Transport),
        Some((scheme, ..)) => Err(ConnectionError::InvalidEndpointScheme(scheme.to_owned())),
        None => Err(ConnectionError::InvalidEndpointScheme("<missing>".to_owned())),
    }
}

/// Uses native roots unless an explicit CA bundle replaces them.
fn build_tls_config(settings: &Settings) -> Result<ClientTlsConfig, ConnectionError> {
    let identity = match (&settings.client_cert.value, &settings.client_key.value) {
        (Some(cert), Some(key)) => Some(Identity::from_pem(
            read_tls_file(cert, "client certificate")?,
            read_tls_file(key, "client key")?,
        )),
        (None, None) => None,
        (..) => return Err(ConnectionError::IncompleteClientIdentity),
    };

    let mut tls = match &settings.ca.value {
        Some(path) => {
            ClientTlsConfig::new().ca_certificate(Certificate::from_pem(read_tls_file(path, "CA certificate bundle")?))
        }
        None => ClientTlsConfig::new().with_native_roots(),
    };

    if let Some(identity) = identity {
        tls = tls.identity(identity);
    }

    Ok(tls)
}

/// Reads one PEM input while preserving its role and path in the error.
fn read_tls_file(path: &Path, kind: &'static str) -> Result<Vec<u8>, ConnectionError> {
    fs::read(path).map_err(|source| ConnectionError::TlsFile { kind, path: path.to_owned(), source })
}

/// Connects with all interceptors pre-applied, given an already-resolved
/// [`Settings`].
///
/// When the resolved timeout is set, it bounds channel establishment and
/// auth setup together, then rides along as the per-request budget of the
/// returned channel. Resolution stays the caller's job so that a transport
/// failure can still be blamed on the alias and URI that were actually
/// resolved, rather than losing that context to a fresh, potentially
/// different resolution.
async fn establish(settings: &Settings) -> Result<LayeredChannel, ConnectionError> {
    // Checked before any I/O, like the TLS/identity checks in
    // `build_endpoint`: a missing cert tag must not cost a round trip or
    // get masked by an unrelated transport failure.
    let resolved_auth = settings.resolved_auth()?;

    let attempt = async {
        let channel = build_endpoint(settings)?.connect().await?;
        let auth = auth::create_layer(resolved_auth).await?;

        Ok::<_, ConnectionError>(auth.layer(channel))
    };

    let auth_service = match settings.timeout.value {
        Some(budget) => tokio::time::timeout(budget, attempt)
            .await
            .map_err(|_| ConnectionError::Timeout(budget))??,
        None => attempt.await?,
    };

    Ok(TimeoutLayer::new(settings.timeout.value).layer(auth_service))
}

/// Resolves the label for an error raised before any RPC is attempted.
///
/// Runs the same resolution [`connect`] runs, without opening a connection:
/// a bad file or an unknown alias becomes the same [`Error`] here as it
/// would from [`Connection::connect_for`], so a configuration mistake exits
/// the same way regardless of where the command first needs an endpoint to
/// reject something.
// The siblings returning the same `Error` are all `async fn`, whose
// desugared `Future` output hides the `Result` from this lint.
#[allow(clippy::result_large_err)]
pub fn resolve_label(args: &ConnectionArgs, action: &str) -> Result<String, Error> {
    config::resolve(args, &config::Sources::from_process_env())
        .map(|settings| settings.label())
        .map_err(|err| Error::from_config(err, action, args.endpoint.clone()))
}

/// Resolves the configuration, then connects with all interceptors
/// pre-applied.
pub async fn connect(args: &ConnectionArgs) -> Result<LayeredChannel, ConnectionError> {
    let settings = config::resolve(args, &config::Sources::from_process_env())?;

    establish(&settings).await
}

/// An established, authenticated channel to one endpoint.
///
/// Connect once, then build one [`Service`] per gRPC service over it with
/// [`Service::new`]. Single-service CLIs skip this and use
/// [`Service::connect_for`], which establishes the connection for them.
pub struct Connection {
    channel: LayeredChannel,
    endpoint: String,
}

impl Connection {
    /// Resolve the target and establish an authenticated channel to it.
    pub async fn connect(args: &ConnectionArgs) -> Result<Self, Error> {
        Self::connect_for(args, "connect").await
    }

    /// Establish the channel, blaming a failure on `action` rather than the
    /// connect step.
    ///
    /// The dynamic-dispatch helpers report the verb the user asked for (e.g.
    /// `ready`) all the way through, so they name their own action here. So
    /// does a CLI that connects once and then dispatches several RPCs over
    /// that one connection: a connect failure must read the same as an RPC
    /// failure of the same command.
    pub async fn connect_for(args: &ConnectionArgs, action: &str) -> Result<Self, Error> {
        let settings = config::resolve(args, &config::Sources::from_process_env())
            .map_err(|err| Error::from_config(err, action, args.endpoint.clone()))?;

        Self::connect_settings(&settings, action).await
    }

    /// Establish the channel from an already-resolved [`Settings`], blaming
    /// a failure on `action`.
    ///
    /// For a caller that has its own reason to resolve first (to reuse the
    /// resolved label, say) rather than resolving twice via
    /// [`Connection::connect_for`].
    pub async fn connect_settings(settings: &Settings, action: &str) -> Result<Self, Error> {
        let endpoint = settings.label();

        establish(settings)
            .await
            .map(|channel| Self { channel, endpoint: endpoint.clone() })
            .map_err(|err| Error::from_connection(err, action, endpoint))
    }

    /// The resolved endpoint this connection reached: `<alias> (<uri>)`
    /// when an alias was used, the bare URI otherwise.
    pub fn endpoint(&self) -> &str {
        &self.endpoint
    }

    /// Invoke a unary RPC on an arbitrary gRPC service over this connection.
    ///
    /// The dispatch is the free [`invoke_unary`]'s, minus the connect: a CLI
    /// probing several services builds one [`Connection`] and calls this per
    /// service, so they all share a single channel.
    pub async fn invoke_unary<Req, Resp>(
        &self,
        action: &str,
        service: &str,
        method: &str,
        request: Req,
    ) -> Result<Resp, Error>
    where
        Req: Message + 'static,
        Resp: Message + Default + 'static,
    {
        let mut grpc = Grpc::new(self.channel.clone())
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);

        let path = PathAndQuery::try_from(format!("/{service}/{method}")).map_err(|err| {
            Error::from_status(
                Status::invalid_argument(err.to_string()),
                action,
                &self.endpoint,
                service,
            )
        })?;

        grpc.ready()
            .await
            .map_err(|err| Error::from_status(Status::unavailable(err.to_string()), action, &self.endpoint, service))?;

        let codec: ProstCodec<Req, Resp> = ProstCodec::default();

        grpc.unary(Request::new(request), path, codec)
            .await
            .map(|response| response.into_inner())
            .map_err(|status| Error::from_status(status, action, &self.endpoint, service))
    }

    /// Invoke a server-streaming RPC on an arbitrary gRPC service over this
    /// connection.
    ///
    /// The dispatch is the free [`invoke_server_stream`]'s, minus the
    /// connect: a CLI watching several services builds one [`Connection`]
    /// and calls this per service, so they all share a single channel.
    /// `tonic::Streaming` never leaks to the caller — all stream state is
    /// contained here.
    pub async fn invoke_server_stream<Req, Resp, F>(
        &self,
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
        let mut grpc = Grpc::new(self.channel.clone())
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);

        let path = PathAndQuery::try_from(format!("/{service}/{method}")).map_err(|err| {
            Error::from_status(
                Status::invalid_argument(err.to_string()),
                action,
                &self.endpoint,
                service,
            )
        })?;

        grpc.ready()
            .await
            .map_err(|err| Error::from_status(Status::unavailable(err.to_string()), action, &self.endpoint, service))?;

        let codec: ProstCodec<Req, Resp> = ProstCodec::default();

        let mut stream = grpc
            .server_streaming(Request::new(request), path, codec)
            .await
            .map_err(|status| Error::from_status(status, action, &self.endpoint, service))?
            .into_inner();

        while let Some(message) = stream
            .message()
            .await
            .map_err(|status| Error::from_status(status, action, &self.endpoint, service))?
        {
            on_message(message);
        }

        Ok(())
    }
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
/// drives several clients over one shared channel builds each from a shared
/// [`Connection`] instead.
pub struct Service<C> {
    client: C,
    endpoint: String,
    name: &'static str,
}

impl<C> Service<C> {
    /// Wrap an already-built `client` with a hand-supplied `endpoint` + `name`.
    ///
    /// Internal building block behind [`Service::new`] / [`Service::connect`]
    /// and the tests; a CLI never wraps a raw client directly.
    pub(crate) fn from_parts(client: C, endpoint: impl Into<String>, name: &'static str) -> Self {
        Self {
            client,
            endpoint: endpoint.into(),
            name,
        }
    }

    /// Wrap a `name` service client over an existing [`Connection`].
    ///
    /// `build` receives a clone of the connection's channel and returns the
    /// tonic-generated client (compression and message sizes are configured
    /// there — they are inherent client methods and cannot be defaulted here).
    /// A CLI driving several services connects once and calls this per client.
    pub fn new<F>(connection: &Connection, name: &'static str, build: F) -> Self
    where
        F: FnOnce(LayeredChannel) -> C,
    {
        Self::from_parts(build(connection.channel.clone()), connection.endpoint.clone(), name)
    }

    /// Connect to `connection` and wrap a single `name` service.
    ///
    /// `name` is the fully-qualified gRPC service name embedded in error
    /// messages. Equivalent to connecting a [`Connection`] and calling
    /// [`Service::new`]; a CLI driving several services does that itself.
    pub async fn connect<F>(connection: &ConnectionArgs, name: &'static str, build: F) -> Result<Self, Error>
    where
        F: FnOnce(LayeredChannel) -> C,
    {
        Ok(Self::new(&Connection::connect(connection).await?, name, build))
    }

    /// Connect to one service with operation-aware failures.
    ///
    /// Transport and service failures carry the same user-facing operation
    /// as the command's RPC failures.
    pub async fn connect_for<F>(
        connection: &ConnectionArgs,
        action: &str,
        name: &'static str,
        build: F,
    ) -> Result<Self, Error>
    where
        F: FnOnce(LayeredChannel) -> C,
    {
        Ok(Self::new(
            &Connection::connect_for(connection, action).await?,
            name,
            build,
        ))
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
    Connection::connect_for(connection, action)
        .await?
        .invoke_unary(action, service, method, request)
        .await
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
    on_message: F,
) -> Result<(), Error>
where
    Req: Message + Send + Sync + 'static,
    Resp: Message + Default + Send + Sync + 'static,
    F: FnMut(Resp),
{
    Connection::connect_for(connection, action)
        .await?
        .invoke_server_stream(action, service, method, request, on_message)
        .await
}

#[cfg(test)]
mod test {
    use core::{net::SocketAddr, time::Duration};
    use std::{
        net::{TcpListener, TcpStream},
        path::PathBuf,
    };

    use socket2::{Domain, Socket, Type};
    use tokio::task::JoinHandle;
    use tokio_stream::wrappers::TcpListenerStream;
    use tonic::{
        transport::{Certificate, Identity, Server, ServerTlsConfig},
        Status,
    };
    use tonic_health::pb::{health_client::HealthClient, HealthCheckRequest};

    use super::{connect, establish, parse_timeout, ConnectionArgs, ConnectionError, Service, TlsArgs};
    use crate::{
        auth::{AuthArgs, AuthMethod},
        config,
        errors::{Error, ErrorKind},
    };

    const TLS_CA: &[u8] = include_bytes!("../tests/fixtures/tls/ca.pem");
    const TLS_SERVER_CERT: &[u8] = include_bytes!("../tests/fixtures/tls/server-cert.pem");
    const TLS_SERVER_KEY: &[u8] = include_bytes!("../tests/fixtures/tls/server-key.pem");

    /// A TLS health server that stops with its owning test.
    struct TestTlsServer {
        endpoint: String,
        task: JoinHandle<()>,
    }

    impl Drop for TestTlsServer {
        fn drop(&mut self) {
            self.task.abort();
        }
    }

    /// Returns a bound health endpoint whose task is owned by the guard.
    async fn start_tls_server(require_client_identity: bool) -> TestTlsServer {
        let listener = tokio::net::TcpListener::bind(("127.0.0.1", 0)).await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let incoming = TcpListenerStream::new(listener);
        let (.., health) = tonic_health::server::health_reporter();
        let identity = Identity::from_pem(TLS_SERVER_CERT, TLS_SERVER_KEY);
        let mut tls = ServerTlsConfig::new().identity(identity);

        if require_client_identity {
            tls = tls.client_ca_root(Certificate::from_pem(TLS_CA));
        }

        let task = tokio::spawn(async move {
            Server::builder()
                .tls_config(tls)
                .unwrap()
                .add_service(health)
                .serve_with_incoming(incoming)
                .await
                .unwrap();
        });

        TestTlsServer {
            endpoint: format!("grpcs://127.0.0.1:{port}"),
            task,
        }
    }

    /// All identities share the checked-in root and remain valid through 4096.
    fn tls_fixture(name: &str) -> PathBuf {
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("tests/fixtures/tls")
            .join(name)
    }

    /// Test connections omit application auth and share a two-second deadline.
    fn tls_connection_args(endpoint: String, tls: TlsArgs) -> ConnectionArgs {
        ConnectionArgs {
            endpoint: Some(endpoint),
            auth: AuthArgs {
                auth: Some(AuthMethod::None),
                cert_tag: None,
            },
            tls,
            timeout: Some(Duration::from_secs(2)),
        }
    }

    /// A successful probe proves the transport reached an authenticated RPC.
    async fn check_tls_health(args: &ConnectionArgs) -> Result<(), Error> {
        let endpoint = args.endpoint.clone().unwrap_or_default();
        let channel = connect(args)
            .await
            .map_err(|err| Error::from_connection(err, "check", &endpoint))?;
        let mut client = HealthClient::new(channel);

        client
            .check(HealthCheckRequest::default())
            .await
            .map_err(|status| Error::from_status(status, "check", &endpoint, "grpc.health.v1.Health"))?;

        Ok(())
    }

    #[test]
    fn test_parse_timeout_accepts_whole_and_fractional_seconds() {
        assert_eq!(Duration::from_secs(3), parse_timeout("3").unwrap());
        assert_eq!(Duration::from_millis(500), parse_timeout("0.5").unwrap());
    }

    #[test]
    fn test_parse_timeout_rejects_non_positive_and_non_numeric() {
        for input in ["0", "-1", "nan", "inf", "abc"] {
            assert!(parse_timeout(input).is_err(), "expected {input} to be rejected");
        }
    }

    /// Verifies that a connect wedged before the TCP handshake completes
    /// fails within the budget and retains the requested operation.
    #[tokio::test]
    async fn test_service_connect_for_tcp_handshake_never_completing_times_out() {
        // A backlog of 0 holds exactly one pending connection on Linux.
        // With that slot taken, further SYNs are silently dropped.
        let socket = Socket::new(Domain::IPV4, Type::STREAM, None).unwrap();
        socket
            .bind(&"127.0.0.1:0".parse::<SocketAddr>().unwrap().into())
            .unwrap();
        socket.listen(0).unwrap();
        let listener: TcpListener = socket.into();
        let port = listener.local_addr().unwrap().port();
        let _plugged = TcpStream::connect(("127.0.0.1", port)).unwrap();

        let args = ConnectionArgs {
            endpoint: Some(format!("grpc://127.0.0.1:{port}")),
            auth: AuthArgs {
                auth: Some(AuthMethod::None),
                cert_tag: None,
            },
            tls: TlsArgs::default(),
            timeout: Some(Duration::from_millis(200)),
        };

        let err = Service::<()>::connect_for(&args, "show", "test.Service", |_| ())
            .await
            .err()
            .unwrap();

        assert_eq!(ErrorKind::Connection, err.kind());
        assert_eq!(4, err.exit_code());
        assert_eq!("show", err.action);
        assert_eq!("connect timed out after 0.2s", err.message());
    }

    #[tokio::test]
    async fn test_connect_rejects_unknown_endpoint_scheme_before_io() {
        let args = ConnectionArgs {
            endpoint: Some("https://127.0.0.1:9".to_owned()),
            auth: AuthArgs {
                auth: Some(AuthMethod::None),
                cert_tag: None,
            },
            tls: TlsArgs {
                ca: Some("missing.pem".into()),
                ..TlsArgs::default()
            },
            timeout: None,
        };

        let err = connect(&args).await.err().unwrap();

        assert!(matches!(err, ConnectionError::InvalidEndpointScheme(scheme) if scheme == "https"));
    }

    #[tokio::test]
    async fn test_connect_rejects_tls_options_for_plaintext_endpoint_before_io() {
        let args = ConnectionArgs {
            endpoint: Some("grpc://127.0.0.1:9".to_owned()),
            auth: AuthArgs {
                auth: Some(AuthMethod::None),
                cert_tag: None,
            },
            tls: TlsArgs {
                ca: Some("missing.pem".into()),
                ..TlsArgs::default()
            },
            timeout: None,
        };

        let err = connect(&args).await.err().unwrap();

        assert!(matches!(err, ConnectionError::TlsOptionsWithoutTls));
    }

    #[tokio::test]
    async fn test_connect_rejects_incomplete_client_identity_before_io() {
        let args = ConnectionArgs {
            endpoint: Some("grpcs://127.0.0.1:9".to_owned()),
            auth: AuthArgs {
                auth: Some(AuthMethod::None),
                cert_tag: None,
            },
            tls: TlsArgs {
                client_cert: Some("missing.pem".into()),
                ..TlsArgs::default()
            },
            timeout: None,
        };

        let err = connect(&args).await.err().unwrap();

        assert!(matches!(err, ConnectionError::IncompleteClientIdentity));
    }

    #[tokio::test]
    async fn test_connect_rejects_sshcert_without_tag_before_io() {
        let args = ConnectionArgs {
            endpoint: Some("grpc://127.0.0.1:9".to_owned()),
            auth: AuthArgs {
                auth: Some(AuthMethod::Sshcert),
                cert_tag: None,
            },
            tls: TlsArgs::default(),
            timeout: None,
        };
        // A real `~/.config/yanet2/cli.yaml` or `/etc/yanet2/cli.yaml`
        // could supply `cert_tag` and mask this on a developer machine, so
        // resolve against locations guaranteed not to exist instead of the
        // real ones `connect` would consult.
        let sources = config::Sources {
            system_path: PathBuf::from("/nonexistent/yanet-cli-test/system.yaml"),
            user_path: None,
            config_override: None,
        };
        let settings = config::resolve(&args, &sources).unwrap();

        let err = establish(&settings).await.err().unwrap();

        assert!(matches!(err, ConnectionError::Config(config::Error::MissingCertTag)));
    }

    #[tokio::test]
    async fn test_connect_custom_ca_and_client_identity_completes_mtls_rpc() {
        let server = start_tls_server(true).await;
        let args = tls_connection_args(
            server.endpoint.clone(),
            TlsArgs {
                ca: Some(tls_fixture("ca.pem")),
                client_cert: Some(tls_fixture("client-cert.pem")),
                client_key: Some(tls_fixture("client-key.pem")),
            },
        );

        check_tls_health(&args).await.unwrap();
    }

    #[tokio::test]
    async fn test_connect_native_roots_reject_untrusted_certificate() {
        let server = start_tls_server(false).await;
        let args = tls_connection_args(server.endpoint.clone(), TlsArgs::default());

        let err = check_tls_health(&args).await.unwrap_err();

        assert!(err.message().to_lowercase().contains("certificate"), "{err:?}");
    }

    #[test]
    fn status_maps_grpc_code() {
        let service = Service::from_parts((), "grpc://[::1]:8080", "test.Service");
        let err = (service.status("list"))(Status::not_found("missing"));

        assert_eq!(ErrorKind::NotFound, err.kind);
    }

    #[test]
    fn invalid_builds_invalid_argument() {
        let service = Service::from_parts((), "grpc://[::1]:8080", "test.Service");
        let err = service.invalid("update", "bad input");

        assert_eq!(ErrorKind::InvalidArgument, err.kind);
        assert_eq!("bad input", err.message);
    }

    #[test]
    fn endpoint_returns_configured_value() {
        let service = Service::from_parts((), "grpc://[::1]:8080", "test.Service");

        assert_eq!("grpc://[::1]:8080", service.endpoint());
    }
}
