//! The error of a C control-plane call.

use core::{
    error::Error,
    ffi::{CStr, c_int},
    fmt::{self, Display, Formatter},
    ptr::{self, NonNull},
};

use yanet_sys::{
    bindings::{self, yanet_error},
    cp::{BuildError, Refusal},
};

/// Why a call failed, as C tags it.
///
/// Mirrors the kinds of the C error type; a kind this crate does not know is
/// [`ErrorKind::Untagged`].
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[non_exhaustive]
pub enum ErrorKind {
    /// No frame of the chain carries a kind.
    Untagged,
    /// The entity the call names does not exist.
    NotFound,
    /// The system is not in the state the call needs.
    FailedPrecondition,
    /// The entity is still referenced.
    Busy,
    /// The request is wrong whatever the state of the system.
    InvalidArgument,
    /// The pool the call draws from has no room left.
    ResourceExhausted,
}

impl ErrorKind {
    fn to_c(self) -> bindings::yanet_error_kind {
        match self {
            Self::Untagged => bindings::yanet_error_kind_YANET_ERROR_NONE,
            Self::NotFound => bindings::yanet_error_kind_YANET_ERROR_NOT_FOUND,
            Self::FailedPrecondition => bindings::yanet_error_kind_YANET_ERROR_FAILED_PRECONDITION,
            Self::Busy => bindings::yanet_error_kind_YANET_ERROR_BUSY,
            Self::InvalidArgument => bindings::yanet_error_kind_YANET_ERROR_INVALID_ARGUMENT,
            Self::ResourceExhausted => bindings::yanet_error_kind_YANET_ERROR_RESOURCE_EXHAUSTED,
        }
    }

    fn from_c(kind: bindings::yanet_error_kind) -> Self {
        match kind {
            bindings::yanet_error_kind_YANET_ERROR_NOT_FOUND => Self::NotFound,
            bindings::yanet_error_kind_YANET_ERROR_FAILED_PRECONDITION => Self::FailedPrecondition,
            bindings::yanet_error_kind_YANET_ERROR_BUSY => Self::Busy,
            bindings::yanet_error_kind_YANET_ERROR_INVALID_ARGUMENT => Self::InvalidArgument,
            bindings::yanet_error_kind_YANET_ERROR_RESOURCE_EXHAUSTED => Self::ResourceExhausted,
            _ => Self::Untagged,
        }
    }
}

/// A failed C call: the error chain C built, freed when dropped.
///
/// C may fail and leave no chain; the error then has no message and no kind.
/// It displays the messages of the chain, outermost first, joined by ": ".
pub struct ApiError {
    chain: Option<NonNull<yanet_error>>,
}

// SAFETY: the value owns its chain alone, save the static out-of-memory frame
// that chains share and nothing frees, and `lib/errors/errors.h` lets any
// thread read a chain and its owner free it from any thread.
unsafe impl Send for ApiError {}

// SAFETY: a shared reference only reads the chain, which `lib/errors/errors.h`
// lets any number of threads do at once.
unsafe impl Sync for ApiError {}

/// Takes ownership of the chain C wrote to an error out-parameter.
///
/// # Safety
///
/// `chain` is null or a live chain whose ownership passes to the error:
/// nothing else frees it or uses it afterwards, save the static out-of-memory
/// frame that chains share and nothing frees.
pub unsafe fn from_c_chain(chain: *mut yanet_error) -> ApiError {
    ApiError { chain: NonNull::new(chain) }
}

/// The chain of an error for C, which then owns it; never null, a call that
/// failed without a chain gets one that says so.
pub fn into_c_chain(mut error: ApiError) -> *mut yanet_error {
    if error.chain.is_none() {
        error = ApiError::new(ErrorKind::Untagged, "the call failed without a message");
    }
    error.chain.take().map_or(ptr::null_mut(), NonNull::as_ptr)
}

/// Adds an error over a chain as one frame of its kind, as C reports it, and
/// of its whole message, so that the chain stays as its cause.
///
/// # Safety
///
/// `*chain` is null or a chain the caller owns.
pub unsafe fn wrap_c_chain(error: ApiError, chain: &mut *mut yanet_error) {
    // SAFETY: caller contract.
    unsafe { add_frame(chain, error.c_kind(), &error.to_string()) };
}

impl ApiError {
    /// An error of one frame, of the given kind and message.
    ///
    /// The message ends at its first NUL byte, as C reads it.
    pub fn new(kind: ErrorKind, message: &str) -> Self {
        with_c_kind(kind.to_c(), message)
    }

    /// The kind of the outermost frame that carries one.
    pub fn kind(&self) -> ErrorKind {
        ErrorKind::from_c(self.c_kind())
    }

    /// The kind C reports for the chain, one this crate does not name
    /// included.
    fn c_kind(&self) -> bindings::yanet_error_kind {
        let Some(chain) = self.chain else {
            return bindings::yanet_error_kind_YANET_ERROR_NONE;
        };
        // SAFETY: the chain is live until drop.
        unsafe { bindings::yanet_error_kind(chain.as_ptr()) }
    }
}

impl From<BuildError<ApiError>> for ApiError {
    fn from(err: BuildError<ApiError>) -> Self {
        match err {
            BuildError::Owner(err) => err,
            BuildError::Refused(refusal) => Self::from(refusal),
            _ => Self::new(
                ErrorKind::Untagged,
                "the build failed for a reason this crate does not name",
            ),
        }
    }
}

impl From<Refusal> for ApiError {
    fn from(refusal: Refusal) -> Self {
        with_c_kind(refusal.kind(), refusal.message())
    }
}

/// A new chain of one frame holding a message, tagged with a kind as C names
/// it, one this crate does not name included.
fn with_c_kind(kind: bindings::yanet_error_kind, message: &str) -> ApiError {
    let mut chain = ptr::null_mut();
    // SAFETY: the chain is null.
    unsafe { add_frame(&mut chain, kind, message) };
    // SAFETY: the chain is the one just made, ours alone.
    unsafe { from_c_chain(chain) }
}

/// Adds a frame holding `message`, which needs no terminator, over the chain
/// at `chain`, as `yanet_error_add` does; a null chain gets a new one.
///
/// # Safety
///
/// `*chain` is null or a chain the caller owns.
unsafe fn add_frame(chain: &mut *mut yanet_error, kind: bindings::yanet_error_kind, message: &str) {
    // SAFETY: the chain by the caller contract, and the format takes the
    // length and the bytes of one string.
    unsafe {
        bindings::yanet_error_add_kind(
            chain,
            kind,
            c"%.*s".as_ptr(),
            message.len().min(c_int::MAX as usize) as c_int,
            message.as_ptr(),
        )
    };
}

impl Drop for ApiError {
    fn drop(&mut self) {
        if let Some(chain) = self.chain {
            // SAFETY: the chain is ours and is not used again.
            unsafe { bindings::yanet_error_free(chain.as_ptr()) };
        }
    }
}

/// Writes a C string, as UTF-8 where it is and U+FFFD where it is not.
fn write_c_str(f: &mut Formatter<'_>, message: &CStr) -> Result<(), fmt::Error> {
    for chunk in message.to_bytes().utf8_chunks() {
        f.write_str(chunk.valid())?;
        if !chunk.invalid().is_empty() {
            f.write_str("\u{FFFD}")?;
        }
    }
    Ok(())
}

impl Display for ApiError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let Some(chain) = self.chain else {
            return f.write_str("the call failed without a message");
        };
        let mut frame = chain.as_ptr().cast_const();
        let mut first = true;
        while !frame.is_null() {
            if !first {
                f.write_str(": ")?;
            }
            first = false;
            // SAFETY: the frame belongs to the live chain, whose frames
            // outlive this borrow of the error.
            let message = unsafe { bindings::yanet_error_message(frame) };
            if !message.is_null() {
                // SAFETY: the message of a live frame is NUL terminated and
                // lives as long as the frame.
                write_c_str(f, unsafe { CStr::from_ptr(message) })?;
            }
            // SAFETY: the frame belongs to the live chain, as for its message.
            frame = unsafe { bindings::yanet_error_cause(frame) };
        }
        Ok(())
    }
}

impl fmt::Debug for ApiError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        f.debug_struct("ApiError")
            .field("kind", &self.kind())
            .field("message", &format_args!("{self}"))
            .finish()
    }
}

impl Error for ApiError {}
