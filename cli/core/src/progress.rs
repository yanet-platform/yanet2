//! Download progress of a response, drawn on stderr while a command waits.

use core::{
    error::Error,
    fmt::{self, Display, Formatter, Write as _},
    future::Future,
    mem,
    pin::Pin,
    sync::atomic::{AtomicBool, AtomicU64, Ordering},
    task::{Context, Poll},
    time::Duration,
};
use std::{
    io::{self, Write},
    thread::{self, JoinHandle},
    time::Instant,
};

use http::{Request, Response};
use http_body::{Frame, SizeHint};
use tonic::codegen::{Body, Bytes};
use tower::Service;

use crate::{
    display,
    output::{self, Paint, Painted},
};

type BoxError = Box<dyn Error + Send + Sync>;

/// Whether a [`Progress`] is drawn, so response bodies report to it.
static ACTIVE: AtomicBool = AtomicBool::new(false);

/// Response bytes received since the progress started.
static RECEIVED: AtomicU64 = AtomicU64::new(0);

/// Size of the response bodies whose gRPC header has arrived.
static TOTAL: AtomicU64 = AtomicU64::new(0);

/// Calls started while the progress is drawn.
static STARTED: AtomicU64 = AtomicU64::new(0);

/// Calls among them whose response ended or failed.
static FINISHED: AtomicU64 = AtomicU64::new(0);

/// How long a wait lasts before anything is drawn, so a fast answer leaves
/// no trace.
const DELAY: Duration = Duration::from_millis(300);

/// How often the line is redrawn.
const TICK: Duration = Duration::from_millis(100);

/// Width of the bar in cells.
const BAR: usize = 24;

const UNICODE_FRAMES: [&str; 10] = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];
const ASCII_FRAMES: [&str; 4] = ["|", "/", "-", "\\"];

/// A progress line on stderr for the calls made while it lives, cleared on
/// drop.
#[must_use = "the line is drawn only while the progress lives"]
pub struct Progress {
    thread: Option<JoinHandle<()>>,
}

impl Progress {
    /// A progress that draws nothing.
    pub fn hidden() -> Self {
        Self { thread: None }
    }

    /// Starts drawing the line with the given message.
    pub fn start(message: String) -> Self {
        RECEIVED.store(0, Ordering::Relaxed);
        TOTAL.store(0, Ordering::Relaxed);
        STARTED.store(0, Ordering::Relaxed);
        FINISHED.store(0, Ordering::Relaxed);
        ACTIVE.store(true, Ordering::Relaxed);

        let thread = thread::spawn(move || draw(&message));

        Self { thread: Some(thread) }
    }
}

impl Drop for Progress {
    fn drop(&mut self) {
        let Some(thread) = self.thread.take() else {
            return;
        };

        ACTIVE.store(false, Ordering::Relaxed);
        thread.thread().unpark();
        let _ = thread.join();
    }
}

/// Redraws the line until the progress is dropped, then clears it.
fn draw(message: &str) {
    let started = Instant::now();
    while ACTIVE.load(Ordering::Relaxed) && started.elapsed() < DELAY {
        thread::park_timeout(DELAY.saturating_sub(started.elapsed()));
    }

    let colored = output::is_colored();
    let frames: &[&str] = if colored { &UNICODE_FRAMES } else { &ASCII_FRAMES };
    let mut stderr = io::stderr();
    let mut buffer = String::new();
    let mut drawn = false;

    for frame in frames.iter().cycle() {
        if !ACTIVE.load(Ordering::Relaxed) {
            break;
        }

        let line = Line {
            frame,
            message,
            received: RECEIVED.load(Ordering::Relaxed),
            total: TOTAL.load(Ordering::Relaxed),
            started: STARTED.load(Ordering::Relaxed),
            finished: FINISHED.load(Ordering::Relaxed),
            colored,
            width: display::stderr_width(),
        };
        // One write per frame, so a slow terminal does not flicker.
        buffer.clear();
        let _ = write!(buffer, "\r\x1B[2K{line}");
        let _ = stderr.write_all(buffer.as_bytes());
        let _ = stderr.flush();
        drawn = true;

        thread::park_timeout(TICK);
    }

    if drawn {
        let _ = write!(stderr, "\r\x1B[2K");
        let _ = stderr.flush();
    }
}

/// One frame of the progress line.
struct Line<'a> {
    frame: &'a str,
    message: &'a str,
    received: u64,
    total: u64,
    started: u64,
    finished: u64,
    colored: bool,
    width: Option<usize>,
}

impl Display for Line<'_> {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let Self {
            frame,
            message,
            received,
            total,
            started,
            finished,
            colored,
            width,
        } = *self;

        let sizes = Sizes { received, total };
        let responses = Responses { started, finished, colored };

        // A wrapped line breaks the redraw, so on a narrow terminal the
        // message gives way first, then the response count, the bar and the
        // sizes, and the frame stays.
        let budget = width.map_or(usize::MAX, |width| width.saturating_sub(1));
        let mut used = 1;
        let mut fits = |len: usize| {
            let fits = len > 0 && used + len <= budget;
            if fits {
                used += len;
            }
            fits
        };
        let show_sizes = fits(sizes.len());
        let show_bar = fits(if total > 0 { 1 + BAR } else { 0 });
        let show_responses = fits(responses.len());
        let room = budget.saturating_sub(used + 1);
        let message = message
            .char_indices()
            .nth(room)
            .map_or(message, |(end, _)| &message[..end]);

        write!(f, "{}", Paint::Dim.when(colored, frame))?;
        if !message.is_empty() {
            write!(f, " {message}")?;
        }

        if show_bar {
            let (full, empty) = if colored { ("█", "░") } else { ("#", "-") };
            let filled = usize::try_from(received.min(total).saturating_mul(BAR as u64) / total).unwrap_or(BAR);

            f.write_str(" ")?;
            for _ in 0..filled {
                f.write_str(full)?;
            }
            for _ in filled..BAR {
                write!(f, "{}", Paint::Dim.when(colored, empty))?;
            }
        }
        if show_sizes {
            let paint = (total == 0).then_some(Paint::Dim).filter(|_| colored);
            write!(f, "{}", Painted::new(paint, sizes))?;
        }
        if show_responses {
            write!(f, "{responses}")?;
        }

        Ok(())
    }
}

/// The downloaded bytes, against the total once it is known.
struct Sizes {
    received: u64,
    total: u64,
}

impl Sizes {
    /// Returns the width of the text in columns.
    fn len(&self) -> usize {
        match (self.received, self.total) {
            (0, 0) => 0,
            (received, 0) => 1 + Size(received).len(),
            (received, total) => 2 + Size(received.min(total)).len() + Size(total).len(),
        }
    }
}

impl Display for Sizes {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match (self.received, self.total) {
            (0, 0) => Ok(()),
            (received, 0) => write!(f, " {}", Size(received)),
            (received, total) => write!(f, " {}/{}", Size(received.min(total)), Size(total)),
        }
    }
}

/// How many of several responses have finished, nothing for one.
struct Responses {
    started: u64,
    finished: u64,
    colored: bool,
}

impl Responses {
    /// Returns the width of the text in columns.
    fn len(&self) -> usize {
        match self.started {
            0 | 1 => 0,
            started => 13 + digits(self.finished.min(started)) + digits(started),
        }
    }
}

impl Display for Responses {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        if self.started < 2 {
            return Ok(());
        }

        write!(
            f,
            "{}",
            Paint::Dim.when(
                self.colored,
                format_args!(", {}/{} responses", self.finished.min(self.started), self.started)
            )
        )
    }
}

/// Returns the number of decimal digits of a value.
fn digits(value: u64) -> usize {
    value.checked_ilog10().map_or(1, |log| log as usize + 1)
}

/// A byte count in MiB with one decimal.
struct Size(u64);

impl Size {
    /// Returns the width of the text in columns.
    fn len(&self) -> usize {
        digits(self.0 / (1 << 20)) + 6
    }
}

impl Display for Size {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let mib = self.0 / (1 << 20);
        let tenth = (self.0 % (1 << 20)) * 10 / (1 << 20);

        write!(f, "{mib}.{tenth} MiB")
    }
}

/// Tower service whose response bodies report their bytes to the running
/// [`Progress`].
#[derive(Clone)]
pub struct ProgressService<S> {
    inner: S,
}

impl<S> ProgressService<S> {
    pub fn new(inner: S) -> Self {
        Self { inner }
    }
}

impl<S, ReqBody, ResBody> Service<Request<ReqBody>> for ProgressService<S>
where
    S: Service<Request<ReqBody>, Response = Response<ResBody>> + Clone + Send + 'static,
    S::Future: Send,
    S::Error: Into<BoxError> + Send,
    ReqBody: Send + 'static,
{
    type Response = Response<ProgressBody<ResBody>>;
    type Error = BoxError;
    type Future = Pin<Box<dyn Future<Output = Result<Self::Response, Self::Error>> + Send>>;

    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(cx).map_err(Into::into)
    }

    fn call(&mut self, request: Request<ReqBody>) -> Self::Future {
        let clone = self.inner.clone();
        let mut inner = mem::replace(&mut self.inner, clone);

        let tracked = ACTIVE.load(Ordering::Relaxed);
        if tracked {
            STARTED.fetch_add(1, Ordering::Relaxed);
        }

        Box::pin(async move {
            match inner.call(request).await {
                Ok(response) => Ok(response.map(|body| ProgressBody::new(body, tracked))),
                Err(err) => {
                    if tracked {
                        FINISHED.fetch_add(1, Ordering::Relaxed);
                    }
                    Err(err.into())
                }
            }
        })
    }
}

/// A unary response body counting its bytes, sized by the length in its
/// five-byte gRPC message header.
pub struct ProgressBody<B> {
    inner: B,
    header: [u8; 5],
    received: u64,
    /// The call started while a progress was drawn and reports to it.
    tracked: bool,
    finished: bool,
}

impl<B> ProgressBody<B> {
    fn new(inner: B, tracked: bool) -> Self {
        Self {
            inner,
            header: [0; 5],
            received: 0,
            tracked,
            finished: false,
        }
    }

    fn finish(&mut self) {
        if self.tracked && !self.finished {
            self.finished = true;
            FINISHED.fetch_add(1, Ordering::Relaxed);
        }
    }

    fn count(&mut self, data: &Bytes) {
        if !self.tracked {
            return;
        }

        let seen = usize::try_from(self.received).unwrap_or(usize::MAX);
        if seen < self.header.len() {
            let take = (self.header.len() - seen).min(data.len());
            self.header[seen..seen + take].copy_from_slice(&data[..take]);

            if seen + take == self.header.len() {
                let [_, length @ ..] = self.header;
                TOTAL.fetch_add(5 + u64::from(u32::from_be_bytes(length)), Ordering::Relaxed);
            }
        }

        self.received = self.received.saturating_add(data.len() as u64);
        RECEIVED.fetch_add(data.len() as u64, Ordering::Relaxed);
    }
}

impl<B> Drop for ProgressBody<B> {
    fn drop(&mut self) {
        self.finish();
    }
}

impl<B> Body for ProgressBody<B>
where
    B: Body<Data = Bytes> + Unpin,
{
    type Data = Bytes;
    type Error = B::Error;

    fn poll_frame(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Self::Data>, Self::Error>>> {
        let frame = Pin::new(&mut self.inner).poll_frame(cx);

        match &frame {
            Poll::Ready(Some(Ok(frame))) => {
                if let Some(data) = frame.data_ref() {
                    self.count(data);
                }
            }
            Poll::Ready(None | Some(Err(..))) => self.finish(),
            Poll::Pending => {}
        }

        frame
    }

    fn is_end_stream(&self) -> bool {
        self.inner.is_end_stream()
    }

    fn size_hint(&self) -> SizeHint {
        self.inner.size_hint()
    }
}
