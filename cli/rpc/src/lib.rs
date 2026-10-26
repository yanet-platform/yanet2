//! The gRPC call path every generated CLI client shares.
//!
//! A generated client hands its request message to [`Client`], which encodes
//! it, runs the call over raw bytes and decodes the response. Everything
//! between the two codecs — the readiness wait, framing, compression and the
//! transport — runs in non-generic functions compiled once in this crate, so a
//! CLI binary compiles only the protobuf code of its own messages per RPC.

use core::{
    future::Future,
    marker::PhantomData,
    pin::Pin,
    task::{Context, Poll},
};

use bytes::{Buf, BufMut, Bytes};
use http::uri::PathAndQuery;
use prost::{DecodeError, Message};
use tonic::{
    GrpcMethod, Request, Response, Status,
    body::Body,
    client::Grpc,
    codec::{Codec, CompressionEncoding, DecodeBuf, Decoder, EncodeBuf, Encoder},
};
use tower::{BoxError, Service, util::BoxCloneSyncService};

type BoxFuture<'a, T> = Pin<Box<dyn Future<Output = T> + Send + 'a>>;

/// The channel generated clients talk through: the transport and every
/// interceptor behind one boxed service.
#[derive(Clone)]
pub struct Channel(BoxCloneSyncService<http::Request<Body>, http::Response<Body>, BoxError>);

impl Channel {
    /// Wraps the service that carries every request.
    pub fn new(service: BoxCloneSyncService<http::Request<Body>, http::Response<Body>, BoxError>) -> Self {
        Self(service)
    }
}

impl Service<http::Request<Body>> for Channel {
    type Response = http::Response<Body>;
    type Error = BoxError;
    type Future = BoxFuture<'static, Result<Self::Response, Self::Error>>;

    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        let Self(service) = self;

        service.poll_ready(cx)
    }

    fn call(&mut self, request: http::Request<Body>) -> Self::Future {
        let Self(service) = self;

        service.call(request)
    }
}

/// One method of a gRPC service, as a generated client names it.
#[derive(Debug)]
pub struct Method {
    service: &'static str,
    name: &'static str,
    path: &'static str,
}

impl Method {
    /// A method `name` of the fully-qualified `service`, requested at `path`,
    /// which is `/<service>/<name>`.
    pub const fn new(service: &'static str, name: &'static str, path: &'static str) -> Self {
        Self { service, name, path }
    }
}

/// A gRPC client over a [`Channel`] with its compression and message size
/// settings.
#[derive(Clone)]
pub struct Client {
    grpc: Grpc<Channel>,
}

impl Client {
    pub fn new(channel: Channel) -> Self {
        Self { grpc: Grpc::new(channel) }
    }

    /// Compresses requests with `encoding`, which the server must accept.
    #[must_use]
    pub fn send_compressed(self, encoding: CompressionEncoding) -> Self {
        Self {
            grpc: self.grpc.send_compressed(encoding),
        }
    }

    /// Accepts responses compressed with `encoding`.
    #[must_use]
    pub fn accept_compressed(self, encoding: CompressionEncoding) -> Self {
        Self {
            grpc: self.grpc.accept_compressed(encoding),
        }
    }

    /// Limits the size of a decoded response message, 4 MiB by default.
    #[must_use]
    pub fn max_decoding_message_size(self, limit: usize) -> Self {
        Self {
            grpc: self.grpc.max_decoding_message_size(limit),
        }
    }

    /// Limits the size of an encoded request message, unlimited by default.
    #[must_use]
    pub fn max_encoding_message_size(self, limit: usize) -> Self {
        Self {
            grpc: self.grpc.max_encoding_message_size(limit),
        }
    }

    /// Calls a unary `method`.
    pub fn unary<Req, Resp>(
        &mut self,
        request: Request<Req>,
        method: &'static Method,
    ) -> impl Future<Output = Result<Response<Resp>, Status>> + Send + '_
    where
        Req: Message,
        Resp: Message + Default,
    {
        let call = self.unary_bytes(request.map(encode), method);

        async move {
            let (metadata, message, extensions) = call.await?.into_parts();

            Ok(Response::from_parts(metadata, decode(message)?, extensions))
        }
    }

    /// Calls a server-streaming `method`.
    pub fn server_streaming<Req, Resp>(
        &mut self,
        request: Request<Req>,
        method: &'static Method,
    ) -> impl Future<Output = Result<Response<Streaming<Resp>>, Status>> + Send + '_
    where
        Req: Message,
        Resp: Message + Default,
    {
        let call = self.server_streaming_bytes(request.map(encode), method);

        async move {
            let response = call.await?;

            Ok(response.map(|messages| Streaming { messages, message: PhantomData }))
        }
    }

    // The calls below carry encoded messages and return boxed futures created
    // here, so their bodies compile once in this crate and never per binary.

    fn unary_bytes(
        &mut self,
        request: Request<Bytes>,
        method: &'static Method,
    ) -> BoxFuture<'_, Result<Response<Bytes>, Status>> {
        Box::pin(async move {
            let (request, path) = self.prepare(request, method).await?;

            self.grpc.unary(request, path, BytesCodec).await
        })
    }

    fn server_streaming_bytes(
        &mut self,
        request: Request<Bytes>,
        method: &'static Method,
    ) -> BoxFuture<'_, Result<Response<tonic::Streaming<Bytes>>, Status>> {
        Box::pin(async move {
            let (request, path) = self.prepare(request, method).await?;

            self.grpc.server_streaming(request, path, BytesCodec).await
        })
    }

    /// Waits for the channel to be ready and tags the request with its method,
    /// as tonic's generated clients do.
    async fn prepare(
        &mut self,
        mut request: Request<Bytes>,
        method: &'static Method,
    ) -> Result<(Request<Bytes>, PathAndQuery), Status> {
        self.grpc
            .ready()
            .await
            .map_err(|err| Status::unknown(format!("Service was not ready: {err}")))?;

        request
            .extensions_mut()
            .insert(GrpcMethod::new(method.service, method.name));

        Ok((request, PathAndQuery::from_static(method.path)))
    }
}

/// The response messages of a server-streaming call.
pub struct Streaming<T> {
    messages: tonic::Streaming<Bytes>,
    message: PhantomData<fn() -> T>,
}

impl<T: Message + Default> Streaming<T> {
    /// The next message, or `None` once the server ends the stream.
    ///
    /// Cancel safe: a dropped call loses no message.
    pub async fn message(&mut self) -> Result<Option<T>, Status> {
        match next(&mut self.messages).await? {
            Some(message) => decode(message).map(Some),
            None => Ok(None),
        }
    }
}

fn next(messages: &mut tonic::Streaming<Bytes>) -> BoxFuture<'_, Result<Option<Bytes>, Status>> {
    Box::pin(messages.message())
}

fn encode<M: Message>(message: M) -> Bytes {
    message.encode_to_vec().into()
}

fn decode<M: Message + Default>(message: Bytes) -> Result<M, Status> {
    M::decode(message).map_err(decode_error)
}

/// A malformed message is an internal error, as tonic's prost codec reports
/// it.
fn decode_error(err: DecodeError) -> Status {
    Status::internal(err.to_string())
}

/// Passes encoded messages through unchanged.
#[derive(Debug, Clone, Copy, Default)]
struct BytesCodec;

impl Codec for BytesCodec {
    type Encode = Bytes;
    type Decode = Bytes;
    type Encoder = Self;
    type Decoder = Self;

    fn encoder(&mut self) -> Self::Encoder {
        Self
    }

    fn decoder(&mut self) -> Self::Decoder {
        Self
    }
}

impl Encoder for BytesCodec {
    type Item = Bytes;
    type Error = Status;

    fn encode(&mut self, item: Bytes, dst: &mut EncodeBuf<'_>) -> Result<(), Status> {
        dst.put(item);

        Ok(())
    }
}

impl Decoder for BytesCodec {
    type Item = Bytes;
    type Error = Status;

    fn decode(&mut self, src: &mut DecodeBuf<'_>) -> Result<Option<Bytes>, Status> {
        Ok(Some(src.copy_to_bytes(src.remaining())))
    }
}
