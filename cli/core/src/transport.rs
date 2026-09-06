use core::{
    pin::Pin,
    task::{Context, Poll, ready},
};
use std::io::{self, IoSlice};

use hyper_util::{client::legacy::connect::HttpConnector, rt::TokioIo};
use socket2::SockRef;
use tokio::{
    io::{AsyncRead, AsyncWrite, Interest, ReadBuf},
    net::TcpStream,
};
use tonic::transport::{Channel, Endpoint, Error};
use tower::ServiceExt;

/// Keeps socket failures observable with the CLI's default SIGPIPE handling.
pub async fn connect(endpoint: Endpoint, unix_path: Option<&str>) -> Result<Channel, Error> {
    if let Some(path) = unix_path {
        return connect_unix(endpoint, path).await;
    }

    let mut http = HttpConnector::new();
    http.enforce_http(false);
    http.set_nodelay(endpoint.get_tcp_nodelay());
    http.set_keepalive(endpoint.get_tcp_keepalive());
    http.set_keepalive_interval(endpoint.get_tcp_keepalive_interval());
    http.set_keepalive_retries(endpoint.get_tcp_keepalive_retries());
    http.set_connect_timeout(endpoint.get_connect_timeout());

    endpoint
        .connect_with_connector(http.map_response(|io| TokioIo::new(SocketIo::Tcp(io.into_inner()))))
        .await
}

#[cfg(unix)]
async fn connect_unix(endpoint: Endpoint, path: &str) -> Result<Channel, Error> {
    let path = path.to_owned();

    endpoint
        .connect_with_connector(tower::service_fn(move |_| {
            let path = path.clone();

            async move {
                tokio::net::UnixStream::connect(path)
                    .await
                    .map(|stream| TokioIo::new(SocketIo::Unix(stream)))
            }
        }))
        .await
}

#[cfg(not(unix))]
async fn connect_unix(endpoint: Endpoint, _path: &str) -> Result<Channel, Error> {
    endpoint.connect().await
}

/// Suppresses SIGPIPE on each socket write so a failed RPC reaches the caller.
///
/// Standard-library socket writes do not consistently suppress the signal
/// across transports and Rust versions. The socket flag preserves the CLI's
/// default signal handling for a closed stdout pipe.
enum SocketIo {
    Tcp(TcpStream),
    #[cfg(unix)]
    Unix(tokio::net::UnixStream),
}

impl SocketIo {
    fn poll_write_ready(&self, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        match self {
            Self::Tcp(stream) => stream.poll_write_ready(cx),
            #[cfg(unix)]
            Self::Unix(stream) => stream.poll_write_ready(cx),
        }
    }

    fn try_write_vectored(&self, bufs: &[IoSlice<'_>]) -> io::Result<usize> {
        #[cfg(unix)]
        let flags = libc::MSG_NOSIGNAL;
        #[cfg(not(unix))]
        let flags = 0;

        match self {
            Self::Tcp(stream) => stream.try_io(Interest::WRITABLE, || {
                SockRef::from(stream).send_vectored_with_flags(bufs, flags)
            }),
            #[cfg(unix)]
            Self::Unix(stream) => stream.try_io(Interest::WRITABLE, || {
                SockRef::from(stream).send_vectored_with_flags(bufs, flags)
            }),
        }
    }
}

impl AsyncRead for SocketIo {
    fn poll_read(self: Pin<&mut Self>, cx: &mut Context<'_>, buf: &mut ReadBuf<'_>) -> Poll<io::Result<()>> {
        match self.get_mut() {
            Self::Tcp(stream) => Pin::new(stream).poll_read(cx, buf),
            #[cfg(unix)]
            Self::Unix(stream) => Pin::new(stream).poll_read(cx, buf),
        }
    }
}

#[allow(clippy::std_instead_of_core)]
impl AsyncWrite for SocketIo {
    fn poll_write(self: Pin<&mut Self>, cx: &mut Context<'_>, buf: &[u8]) -> Poll<io::Result<usize>> {
        self.poll_write_vectored(cx, &[IoSlice::new(buf)])
    }

    fn poll_write_vectored(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        bufs: &[IoSlice<'_>],
    ) -> Poll<io::Result<usize>> {
        loop {
            ready!(self.poll_write_ready(cx))?;

            match self.try_write_vectored(bufs) {
                Err(error) if error.kind() == io::ErrorKind::WouldBlock => continue,
                result => return Poll::Ready(result),
            }
        }
    }

    fn is_write_vectored(&self) -> bool {
        true
    }

    fn poll_flush(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Poll::Ready(Ok(()))
    }

    fn poll_shutdown(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        match self.get_mut() {
            Self::Tcp(stream) => Pin::new(stream).poll_shutdown(cx),
            #[cfg(unix)]
            Self::Unix(stream) => Pin::new(stream).poll_shutdown(cx),
        }
    }
}

#[cfg(all(test, unix))]
mod test {
    use core::{future::Future, time::Duration};
    use std::{
        io::IoSlice,
        net::{Shutdown, TcpListener, TcpStream},
        process::Command,
    };

    use socket2::SockRef;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    use super::SocketIo;

    /// Isolates signal handling so a regression cannot kill other tests.
    fn with_default_sigpipe(name: &str, run: impl Future<Output = ()>) {
        const CHILD_ENV: &str = "YANET_TEST_SOCKET_SIGPIPE";

        if std::env::var(CHILD_ENV).as_deref() == Ok(name) {
            crate::signal::init();
            tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()
                .unwrap()
                .block_on(run);
            return;
        }

        let output = Command::new(std::env::current_exe().unwrap())
            .args(["--exact", name, "--nocapture"])
            .env(CHILD_ENV, name)
            .output()
            .unwrap();

        assert!(
            output.status.success(),
            "{name} exited with {}: {}",
            output.status,
            String::from_utf8_lossy(&output.stderr)
        );
        assert!(String::from_utf8_lossy(&output.stdout).contains("1 passed"));
    }

    #[test]
    fn test_socket_io_closed_unix_peer_returns_broken_pipe() {
        with_default_sigpipe(
            "transport::test::test_socket_io_closed_unix_peer_returns_broken_pipe",
            async {
                let (stream, peer) = tokio::net::UnixStream::pair().unwrap();
                drop(peer);

                let mut stream = SocketIo::Unix(stream);
                let error = stream.write(b"request").await.unwrap_err();
                assert_eq!(Some(libc::EPIPE), error.raw_os_error());

                let error = stream
                    .write_vectored(&[IoSlice::new(b"request"), IoSlice::new(b"body")])
                    .await
                    .unwrap_err();

                assert_eq!(Some(libc::EPIPE), error.raw_os_error());
            },
        );
    }

    #[test]
    fn test_socket_io_shutdown_tcp_socket_returns_broken_pipe() {
        with_default_sigpipe(
            "transport::test::test_socket_io_shutdown_tcp_socket_returns_broken_pipe",
            async {
                let listener = TcpListener::bind("127.0.0.1:0").unwrap();
                let stream = TcpStream::connect(listener.local_addr().unwrap()).unwrap();
                let (_peer, _) = listener.accept().unwrap();
                stream.shutdown(Shutdown::Write).unwrap();
                stream.set_nonblocking(true).unwrap();
                let stream = tokio::net::TcpStream::from_std(stream).unwrap();

                let mut stream = SocketIo::Tcp(stream);
                let error = stream.write(b"request").await.unwrap_err();
                assert_eq!(Some(libc::EPIPE), error.raw_os_error());

                let error = stream
                    .write_vectored(&[IoSlice::new(b"request"), IoSlice::new(b"body")])
                    .await
                    .unwrap_err();

                assert_eq!(Some(libc::EPIPE), error.raw_os_error());
            },
        );
    }

    #[tokio::test]
    async fn test_socket_io_partial_writes_resume_without_losing_data() {
        let (stream, mut peer) = tokio::net::UnixStream::pair().unwrap();
        SockRef::from(&stream).set_send_buffer_size(1024).unwrap();
        let mut stream = SocketIo::Unix(stream);
        let first = vec![0xa5; 64 * 1024];
        let second = vec![0x5a; 64 * 1024];

        tokio::time::timeout(Duration::from_secs(5), async {
            let send = async {
                let mut bufs = [IoSlice::new(&[]), IoSlice::new(&first), IoSlice::new(&second)];
                let mut remaining = bufs.as_mut_slice();

                while !remaining.is_empty() {
                    let written = stream.write_vectored(remaining).await.unwrap();
                    assert!(written > 0);
                    IoSlice::advance_slices(&mut remaining, written);
                }

                stream.write_all(b"tail").await.unwrap();
                stream.shutdown().await.unwrap();
            };
            let receive = async {
                let mut received = Vec::new();
                peer.read_to_end(&mut received).await.unwrap();
                assert_eq!([first.as_slice(), second.as_slice(), b"tail"].concat(), received);
            };

            tokio::join!(send, receive);
        })
        .await
        .expect("socket I/O stalled under backpressure");
    }
}
