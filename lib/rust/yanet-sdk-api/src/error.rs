//! The error of a C control-plane call.

use core::{
    error::Error,
    ffi::{CStr, c_char, c_int},
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

// SAFETY: error frames are immutable and their heap chain is exclusively owned.
//
// Any thread may release it; the static out-of-memory frame is never freed.
unsafe impl Send for ApiError {}

// SAFETY: as for `Send`, a shared reference only reads the frames.
unsafe impl Sync for ApiError {}

/// Takes exclusive ownership of a native error chain.
///
/// # Safety
///
/// The input must be null or identify a valid chain that no other owner will
/// free.
pub unsafe fn from_c_chain(chain: *mut yanet_error) -> ApiError {
    ApiError { chain: NonNull::new(chain) }
}

pub fn tagged(kind: ErrorKind, message: &CStr) -> ApiError {
    let mut chain = ptr::null_mut();
    // SAFETY: the out-parameter points at a null chain, the format takes one
    // string, and the message is NUL terminated.
    unsafe { bindings::yanet_error_add_kind(&mut chain, kind.to_c(), c"%s".as_ptr(), message.as_ptr()) };
    // SAFETY: the chain is the one just made, ours alone.
    unsafe { from_c_chain(chain) }
}

impl ApiError {
    /// An error of one frame, of the given kind, for a method that refuses its
    /// input.
    pub fn new(kind: ErrorKind, message: &str) -> Self {
        from_str(kind, message)
    }

    /// The chain for C, which then owns it; never null, a call that failed
    /// without a chain gets one that says so.
    pub fn into_c_chain(mut self) -> *mut yanet_error {
        if self.chain.is_none() {
            self = from_str(ErrorKind::Untagged, "the call failed without a message");
        }
        self.chain.take().map_or(ptr::null_mut(), NonNull::as_ptr)
    }

    /// The kind of the outermost frame that carries one.
    pub fn kind(&self) -> ErrorKind {
        let Some(chain) = self.chain else {
            return ErrorKind::Untagged;
        };
        // SAFETY: the chain is live until drop.
        ErrorKind::from_c(unsafe { bindings::yanet_error_kind(chain.as_ptr()) })
    }
}

impl From<BuildError<ApiError>> for ApiError {
    fn from(err: BuildError<ApiError>) -> Self {
        match err {
            BuildError::Owner(err) => err,
            BuildError::Refused(refusal) => Self::from(refusal),
            _ => tagged(
                ErrorKind::Untagged,
                c"the build failed for a reason this crate does not name",
            ),
        }
    }
}

// The message is the refusal's own, tagged with the kind its documentation
// names.
impl From<Refusal> for ApiError {
    fn from(refusal: Refusal) -> Self {
        let kind = match refusal {
            Refusal::OutOfMemory => ErrorKind::ResourceExhausted,
            _ => ErrorKind::InvalidArgument,
        };
        from_str(kind, refusal.message())
    }
}

/// Creates one error frame from text, stopping at its first NUL byte.
pub fn from_str(kind: ErrorKind, message: &str) -> ApiError {
    let mut chain = ptr::null_mut();
    // SAFETY: the out-parameter points at a null chain, and the format takes
    // the length and the bytes of one string.
    unsafe {
        bindings::yanet_error_add_kind(
            &mut chain,
            kind.to_c(),
            c"%.*s".as_ptr(),
            message.len().min(c_int::MAX as usize) as c_int,
            message.as_ptr(),
        )
    };
    // SAFETY: the chain is the one just made, ours alone.
    unsafe { from_c_chain(chain) }
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
fn write_c_str(f: &mut Formatter<'_>, message: *const c_char) -> Result<(), fmt::Error> {
    if message.is_null() {
        return Ok(());
    }
    // SAFETY: a non-null message of a live frame is NUL terminated and lives
    // as long as the frame.
    let bytes = unsafe { CStr::from_ptr(message) }.to_bytes();
    for chunk in bytes.utf8_chunks() {
        f.write_str(chunk.valid())?;
        if !chunk.invalid().is_empty() {
            f.write_str(char::REPLACEMENT_CHARACTER.encode_utf8(&mut [0; 4]))?;
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
            write_c_str(f, unsafe { bindings::yanet_error_message(frame) })?;
            // SAFETY: as above.
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
