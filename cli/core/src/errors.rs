//! Structured CLI error type.
//!
//! [`Error`] carries the action being performed, the error kind (mapped
//! from `tonic::Code` or `ConnectionError`), the raw message, and optional
//! contextual fields (`endpoint`, `service`, `hint`).
//!
//! To render an error call `output::failure(&err)` — that function owns all
//! formatting logic and reads the global output context set by `output::init`.

use core::fmt::{self, Display, Formatter};

use tonic::Code;

use crate::client::ConnectionError;

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
    /// TCP / TLS connection could not be established.
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
        let message = status.message().to_owned();
        let code = status.code();

        let kind = map_code(code, &message);
        let hint = default_hint(kind);

        Self {
            action,
            kind,
            message: message.clone(),
            endpoint: Some(endpoint),
            service: Some(service),
            hint,
            raw_code: Some(format!("{code:?}")),
            raw_message: Some(message),
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
            ConnectionError::InvalidUri(..) => (
                ErrorKind::InvalidArgument,
                Some("check the --endpoint URL format (expected: grpc://host:port or unix:///path)".to_owned()),
            ),
            ConnectionError::Auth(..) => (ErrorKind::Auth, None),
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

    /// Process exit code for this error.
    pub fn exit_code(&self) -> i32 {
        self.kind.exit_code()
    }
}

/// Map a `tonic::Code` (and optional message text) to a [`ErrorKind`].
fn map_code(code: Code, message: &str) -> ErrorKind {
    match code {
        Code::NotFound if message.contains("unknown service") => ErrorKind::ServiceUnregistered,
        Code::NotFound => ErrorKind::NotFound,
        Code::InvalidArgument => ErrorKind::InvalidArgument,
        Code::Unauthenticated | Code::PermissionDenied => ErrorKind::Auth,
        Code::Unavailable => ErrorKind::Unavailable,
        _ => ErrorKind::Rpc,
    }
}

/// Build a default hint for the given error kind.
fn default_hint(kind: ErrorKind) -> Option<String> {
    match kind {
        ErrorKind::ServiceUnregistered => Some(
            "the operator may be down or not yet registered with the gateway\n\
             (check available services: yanet-cli inspect services)"
                .to_owned(),
        ),
        ErrorKind::Connection => Some("verify the endpoint is reachable and the gateway is up".to_owned()),
        _ => None,
    }
}
