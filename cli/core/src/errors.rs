//! Structured CLI error type.
//!
//! [`Error`] carries the action being performed, the error kind (mapped
//! from `tonic::Code` or `ConnectionError`), the raw message, and optional
//! contextual fields (`endpoint`, `service`, `hint`).
//!
//! To render an error call `output::failure(&err)` — that function owns all
//! formatting logic and reads the global output context set by `output::init`.

use core::fmt::{self, Display, Formatter};

use tonic::{Code, Status};

use crate::{client::ConnectionError, config};

/// Category of a CLI-level error, derived from the underlying `tonic::Code`
/// or transport failure.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ErrorKind {
    /// Service is not registered at the gateway endpoint.
    ServiceUnregistered,
    /// Resource was not found.
    NotFound,
    /// The request contained invalid arguments.
    InvalidArgument,
    /// Authentication or authorization failure.
    Auth,
    /// The endpoint is reachable but the service is unavailable.
    Unavailable,
    /// A gRPC-level error not covered by other variants.
    Rpc,
    /// A transport connection to the endpoint could not be established.
    Connection,
}

impl ErrorKind {
    /// Process exit code for this error kind.
    pub fn exit_code(self) -> i32 {
        match self {
            Self::ServiceUnregistered | Self::NotFound => 3,
            Self::InvalidArgument | Self::Auth | Self::Rpc => 1,
            Self::Unavailable | Self::Connection => 4,
        }
    }

    /// Snake-case string for the JSON `kind` field.
    pub(crate) fn as_str(self) -> &'static str {
        match self {
            Self::ServiceUnregistered => "service_unregistered",
            Self::NotFound => "not_found",
            Self::InvalidArgument => "invalid_argument",
            Self::Auth => "auth",
            Self::Unavailable => "unavailable",
            Self::Rpc => "rpc",
            Self::Connection => "connection",
        }
    }
}

impl Display for ErrorKind {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        f.write_str(self.as_str())
    }
}

/// A structured CLI error ready to be rendered via `output::failure`.
#[derive(Debug)]
pub struct Error {
    /// The user-facing action being attempted (e.g. `"insert"`, `"show"`).
    pub(crate) action: String,
    /// Categorised error kind.
    pub(crate) kind: ErrorKind,
    /// Human-readable error message from the RPC or transport layer.
    pub(crate) message: String,
    /// gRPC endpoint URL, if known.
    pub(crate) endpoint: Option<String>,
    /// Fully-qualified gRPC service name, if applicable.
    pub(crate) service: Option<String>,
    /// Optional hint text shown after the key/value block.
    ///
    /// Newlines in the hint are followed by continuation lines aligned to
    /// column 11.
    pub(crate) hint: Option<String>,
    /// Raw `tonic::Status` code name (set when the error originated from
    /// a gRPC status).
    pub(crate) raw_code: Option<String>,
    /// Raw gRPC message (may differ from `message` after normalisation).
    pub(crate) raw_message: Option<String>,
}

impl Error {
    /// Build an [`Error`] from a `tonic::Status`.
    ///
    /// `action` is the user-facing verb (e.g. `"show"`).
    /// `endpoint` and `service` provide context for the error block.
    pub fn from_status(
        status: tonic::Status,
        action: impl Into<String>,
        endpoint: impl Into<String>,
        service: impl Into<String>,
    ) -> Self {
        let action = action.into();
        let endpoint = endpoint.into();
        let service = service.into();
        let raw_message = status.message().to_owned();
        let message = match (raw_message.as_str(), core::error::Error::source(&status)) {
            ("transport error", Some(source)) => root_cause(source).to_string(),
            (..) => raw_message.clone(),
        };
        let code = status.code();

        let kind = map_code(&status);
        let hint = default_hint(kind);

        Self {
            action,
            kind,
            message: message.clone(),
            endpoint: Some(endpoint),
            service: Some(service),
            hint,
            raw_code: Some(format!("{code:?}")),
            raw_message: Some(raw_message),
        }
    }

    /// Build an [`Error`] from a [`ConnectionError`].
    pub fn from_connection(err: ConnectionError, action: impl Into<String>, endpoint: impl Into<String>) -> Self {
        let action = action.into();
        let endpoint = endpoint.into();
        let message = err.to_string();

        let (kind, hint) = match err {
            ConnectionError::Transport(..) => (
                ErrorKind::Connection,
                Some("verify the endpoint is reachable and the gateway is up".to_owned()),
            ),
            ConnectionError::InvalidUri(..) | ConnectionError::InvalidEndpointScheme(..) => (
                ErrorKind::InvalidArgument,
                Some(
                    "check the --endpoint URL format (expected: grpc://host:port, grpcs://host:port, or unix:///path)"
                        .to_owned(),
                ),
            ),
            ConnectionError::TlsOptionsWithoutTls
            | ConnectionError::IncompleteClientIdentity
            | ConnectionError::TlsFile { .. }
            | ConnectionError::TlsConfig(..) => (ErrorKind::InvalidArgument, None),
            ConnectionError::Auth(..) => (ErrorKind::Auth, None),
            ConnectionError::Timeout(..) => (
                ErrorKind::Connection,
                Some("verify the endpoint is reachable and the gateway is up".to_owned()),
            ),
            ConnectionError::Config(ref config_err) => (ErrorKind::InvalidArgument, config_error_hint(config_err)),
        };

        Self {
            action,
            kind,
            message,
            endpoint: Some(endpoint),
            service: None,
            hint,
            raw_code: None,
            raw_message: None,
        }
    }

    /// Build an [`Error`] straight from a [`config::Error`], without the
    /// endpoint a call site has no real value for.
    ///
    /// Pass `None` when nothing was ever typed for the endpoint, such as an
    /// unreadable or malformed file. Pass `args.endpoint.clone()` otherwise,
    /// since even an alias that turned out unknown is worth showing.
    pub fn from_config(err: config::Error, action: impl Into<String>, endpoint: Option<String>) -> Self {
        let action = action.into();
        let message = err.to_string();
        let hint = config_error_hint(&err);

        Self {
            action,
            kind: ErrorKind::InvalidArgument,
            message,
            endpoint,
            service: None,
            hint,
            raw_code: None,
            raw_message: None,
        }
    }

    /// Build an [`Error`] the CLI decided on itself, carrying an explicit
    /// `kind`.
    ///
    /// No RPC was issued, so there is no `tonic::Status` to map from and no
    /// service to name. Pass the `kind` the gateway would have reported for
    /// the same condition — an alias resolved against the live registry that
    /// matches nothing means exactly what the gateway's `unknown service`
    /// means — so that both spellings of a condition exit with one code.
    /// [`Error::invalid_argument`] is the shorthand for the common case.
    pub fn new(
        kind: ErrorKind,
        action: impl Into<String>,
        endpoint: impl Into<String>,
        message: impl Into<String>,
    ) -> Self {
        Self {
            action: action.into(),
            kind,
            message: message.into(),
            endpoint: Some(endpoint.into()),
            service: None,
            hint: default_hint(kind),
            raw_code: None,
            raw_message: None,
        }
    }

    /// Build an [`Error`] for input the CLI rejected before issuing any RPC.
    ///
    /// Unlike [`Error::from_status`] no service is named: the input was wrong
    /// on its own terms — a malformed value, an impossible flag combination —
    /// so the endpoint is the only context worth carrying.
    pub fn invalid_argument(
        action: impl Into<String>,
        endpoint: impl Into<String>,
        message: impl Into<String>,
    ) -> Self {
        Self::new(ErrorKind::InvalidArgument, action, endpoint, message)
    }

    /// Attach `hint`, replacing the default hint for this error kind.
    ///
    /// For the call sites that know more than the generic status mapping can
    /// — the readiness CLI, say, which can name the services the gateway
    /// actually has. A multi-line hint renders as aligned continuation lines.
    pub fn with_hint(mut self, hint: impl Into<String>) -> Self {
        self.hint = Some(hint.into());

        self
    }

    /// The category this error was mapped to.
    pub fn kind(&self) -> ErrorKind {
        self.kind
    }

    /// The human-readable message from the RPC or transport layer.
    pub fn message(&self) -> &str {
        &self.message
    }

    /// Process exit code for this error.
    pub fn exit_code(&self) -> i32 {
        self.kind.exit_code()
    }
}

/// Trailer metadata key the gateway sets on a `NotFound` `Status` when the
/// requested gRPC service has no backend registered.
///
/// This is a wire contract with the gateway rather than a local constant:
/// changing it stops registry misses being recognised, silently.
const ERROR_REASON_METADATA_KEY: &str = "x-yanet-error-reason";

/// Value of [`ERROR_REASON_METADATA_KEY`] that marks a registry miss.
const SERVICE_UNREGISTERED_REASON: &str = "service-unregistered";

/// Prefix a registry-miss `NotFound` message opens with, on a gateway that
/// predates [`ERROR_REASON_METADATA_KEY`].
///
/// During a rolling upgrade a CLI built after the trailer landed can still
/// talk to a gateway peer built before it, and that older peer never attaches
/// the trailer. [`is_unknown_service_status`] falls back to this marker so
/// the miss is still recognised in that window. Drop the constant and its use
/// once no supported gateway predates the trailer.
const UNKNOWN_SERVICE_MARKER: &str = "unknown service";

/// Reports whether `status` is the gateway's unregistered-service `NotFound`.
///
/// The trailer set by [`ERROR_REASON_METADATA_KEY`] is the primary signal, so
/// the gateway's human-readable message stays free to change without
/// silently reverting the classification client-side. A status without the
/// trailer is still recognised when its message starts with
/// [`UNKNOWN_SERVICE_MARKER`] — the compatibility fallback described there.
/// That check is anchored at the start of the message (`starts_with`, not
/// `contains`): a resource `NotFound` that merely quotes the marker inside a
/// config name (e.g. `config "unknown service" not found`) carries it
/// mid-message and must stay a plain `NotFound`.
fn is_unknown_service_status(status: &Status) -> bool {
    let has_trailer = status
        .metadata()
        .get(ERROR_REASON_METADATA_KEY)
        .is_some_and(|value| value == SERVICE_UNREGISTERED_REASON);

    has_trailer || status.message().starts_with(UNKNOWN_SERVICE_MARKER)
}

/// Map a `tonic::Status` to an [`ErrorKind`].
fn map_code(status: &Status) -> ErrorKind {
    match status.code() {
        Code::NotFound if is_unknown_service_status(status) => ErrorKind::ServiceUnregistered,
        Code::NotFound => ErrorKind::NotFound,
        Code::InvalidArgument => ErrorKind::InvalidArgument,
        Code::Unauthenticated | Code::PermissionDenied => ErrorKind::Auth,
        Code::Unavailable => ErrorKind::Unavailable,
        _ => ErrorKind::Rpc,
    }
}

/// Returns the innermost cause, the one text that explains a failure whose
/// wrappers only label the layer they come from.
pub fn root_cause<'a>(err: &'a (dyn core::error::Error + 'static)) -> &'a (dyn core::error::Error + 'static) {
    let mut err = err;

    while let Some(source) = err.source() {
        err = source;
    }

    err
}

/// Builds the hint for a [`ConnectionError::Config`] failure.
///
/// The message of [`config::Error::UnknownAlias`] already lists the known
/// aliases, so no separate hint is needed there. A file-reading or -parsing
/// failure adds one naming the offending path.
fn config_error_hint(err: &config::Error) -> Option<String> {
    match err {
        config::Error::Read { path, .. }
        | config::Error::Parse { path, .. }
        | config::Error::InvalidTimeout { path, .. } => {
            Some(format!("check the configuration file at {}", path.display()))
        }
        config::Error::SectionMissingEndpoint { path, .. } => {
            Some(format!("add an \"endpoint\" key to that alias in {}", path.display()))
        }
        config::Error::UnknownAlias { .. } | config::Error::AliasChain { .. } | config::Error::MissingCertTag => None,
    }
}

/// Build a default hint for the given error kind.
fn default_hint(kind: ErrorKind) -> Option<String> {
    match kind {
        ErrorKind::ServiceUnregistered => Some(
            "the operator may be down or not yet registered with the gateway\n\
             (check available services: yanet-cli gateway list)"
                .to_owned(),
        ),
        ErrorKind::Connection => Some("verify the endpoint is reachable and the gateway is up".to_owned()),
        _ => None,
    }
}

/// Maps a gRPC status to a resource-specific not-found error for one service.
pub struct NotFoundMapper {
    service: &'static str,
    default_resource: &'static str,
}

impl NotFoundMapper {
    /// Creates a mapper for the given service and default resource label.
    pub const fn new(service: &'static str, default_resource: &'static str) -> Self {
        Self { service, default_resource }
    }

    /// Maps `status` to an [`Error`], rewriting a genuine resource `NotFound`
    /// into a friendly `"<resource> not found"` message and passing everything
    /// else (including a registry-miss `NotFound`, whether recognised by
    /// trailer or by the compatibility fallback) through unchanged.
    pub fn map(
        &self,
        status: Status,
        action: impl Into<String>,
        endpoint: impl Into<String>,
        resource: Option<&str>,
    ) -> Error {
        if status.code() == Code::NotFound && !is_unknown_service_status(&status) {
            let resource = resource.unwrap_or(self.default_resource);

            return Error::from_status(
                Status::not_found(format!("{resource} not found")),
                action,
                endpoint,
                self.service,
            );
        }

        Error::from_status(status, action, endpoint, self.service)
    }
}

#[cfg(test)]
mod test {
    use std::sync::Arc;

    use tonic::{metadata::MetadataMap, Code, Status};

    use super::{Error, ErrorKind, NotFoundMapper};

    const MAPPER: NotFoundMapper = NotFoundMapper::new("test.Service", "requested item");

    /// Builds a `NotFound` `Status` carrying the gateway's registry-miss
    /// trailer, pinned to the literal wire values rather than the
    /// production constants so the test exercises the actual contract.
    fn unregistered_status(message: &str) -> Status {
        let mut metadata = MetadataMap::new();
        metadata.insert("x-yanet-error-reason", "service-unregistered".parse().unwrap());

        Status::with_metadata(Code::NotFound, message, metadata)
    }

    #[test]
    fn new_carries_the_requested_kind() {
        let err = Error::new(
            ErrorKind::ServiceUnregistered,
            "ready",
            "grpc://[::1]:8080",
            "unknown readiness service",
        );

        assert_eq!(ErrorKind::ServiceUnregistered, err.kind());
        assert_eq!(3, err.exit_code());
    }

    #[test]
    fn invalid_argument_is_an_invalid_argument() {
        let err = Error::invalid_argument("ready", "grpc://[::1]:8080", "bad input");

        assert_eq!(ErrorKind::InvalidArgument, err.kind());
        assert_eq!(1, err.exit_code());
    }

    #[test]
    fn not_found_resource_is_rewritten() {
        let status = Status::not_found("some resource missing");
        let err = MAPPER.map(status, "show item", "http://localhost:50051", Some("item 'x'"));

        assert_eq!(ErrorKind::NotFound, err.kind);
        assert_eq!("item 'x' not found", err.message);
    }

    #[test]
    fn not_found_uses_default_resource_when_none_given() {
        let status = Status::not_found("some resource missing");
        let err = MAPPER.map(status, "show item", "http://localhost:50051", None);

        assert_eq!(ErrorKind::NotFound, err.kind);
        assert_eq!("requested item not found", err.message);
    }

    #[test]
    fn unknown_service_not_found_passes_through_as_service_unregistered() {
        let status = unregistered_status("unknown service test.Service");
        let err = MAPPER.map(status, "show item", "http://localhost:50051", None);

        assert_eq!(ErrorKind::ServiceUnregistered, err.kind);
    }

    #[test]
    fn unknown_service_message_without_the_trailer_is_the_compatibility_fallback() {
        let status = Status::not_found("unknown service");
        let err = MAPPER.map(status, "show item", "http://localhost:50051", None);

        assert_eq!(ErrorKind::ServiceUnregistered, err.kind);
    }

    #[test]
    fn trailer_is_service_unregistered_even_with_a_config_name_message() {
        let status = unregistered_status(r#"config "unknown service" not found"#);
        let err = MAPPER.map(status, "show item", "http://localhost:50051", Some("item 'x'"));

        assert_eq!(ErrorKind::ServiceUnregistered, err.kind);
    }

    #[test]
    fn config_name_containing_the_marker_is_not_service_unregistered() {
        let status = Status::not_found(r#"config "unknown service" not found"#);
        let err = Error::from_status(status, "show", "grpc://[::1]:8080", "test.Service");

        assert_eq!(ErrorKind::NotFound, err.kind());
    }

    #[test]
    fn config_name_containing_the_marker_is_rewritten_by_the_mapper() {
        let status = Status::not_found(r#"config "unknown service" not found"#);
        let err = MAPPER.map(status, "show item", "http://localhost:50051", Some("item 'x'"));

        assert_eq!(ErrorKind::NotFound, err.kind);
        assert_eq!("item 'x' not found", err.message);
    }

    #[test]
    fn non_not_found_status_passes_through_unchanged() {
        let status = Status::unavailable("backend down");
        let err = MAPPER.map(status, "show item", "http://localhost:50051", None);

        assert_eq!(ErrorKind::Unavailable, err.kind);
    }

    #[test]
    fn test_from_status_exposes_root_source_and_preserves_raw_message() {
        let mut status = Status::unknown("transport error");
        status.set_source(Arc::new(std::io::Error::other(
            "received fatal alert: CertificateRequired",
        )));

        let err = Error::from_status(status, "check", "grpcs://gateway.test", "test.Service");

        assert_eq!("received fatal alert: CertificateRequired", err.message);
        assert_eq!(Some("transport error".to_owned()), err.raw_message);
    }

    #[test]
    fn test_from_status_preserves_specific_message_with_source() {
        let mut status = Status::internal("h2 protocol error");
        status.set_source(Arc::new(std::io::Error::other("connection reset")));

        let err = Error::from_status(status, "check", "grpcs://gateway.test", "test.Service");

        assert_eq!("h2 protocol error", err.message);
        assert_eq!(Some("h2 protocol error".to_owned()), err.raw_message);
    }
}
