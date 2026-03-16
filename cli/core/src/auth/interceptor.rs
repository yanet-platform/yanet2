//! Tonic gRPC interceptor for SSH certificate authentication.

use std::{
    future::Future,
    pin::Pin,
    sync::Arc,
    task::{Context, Poll},
};

use tokio::sync::Mutex;
use tower::{Layer, Service};

use super::{
    agent::{AgentError, SshAgent},
    token::Token,
};

/// Tower layer that wraps a service with SSH certificate authentication.
#[derive(Clone)]
pub struct AuthLayer {
    state: Arc<Mutex<AuthLayerState>>,
}

enum AuthLayerState {
    SshCert { agent: SshAgent, cert_blob: Vec<u8> },
    Nop,
}

impl AuthLayer {
    /// Create an auth layer that signs requests using the SSH agent.
    pub fn sshcert(agent: SshAgent, cert_blob: Vec<u8>) -> Self {
        Self {
            state: Arc::new(Mutex::new(AuthLayerState::SshCert { agent, cert_blob })),
        }
    }

    /// Create an auth layer from the SSH agent, finding a certificate
    /// matching the given tag.
    pub async fn from_agent(cert_tag: &str) -> Result<Self, AgentError> {
        let mut agent = SshAgent::from_env().await?;
        let (_cert, blob) = agent.find_certificate(cert_tag).await?;
        Ok(Self::sshcert(agent, blob))
    }

    /// Create a no-op auth layer that passes requests through unchanged.
    pub fn nop() -> Self {
        Self {
            state: Arc::new(Mutex::new(AuthLayerState::Nop)),
        }
    }
}

impl<S> Layer<S> for AuthLayer {
    type Service = AuthService<S>;

    fn layer(&self, inner: S) -> Self::Service {
        AuthService { inner, state: self.state.clone() }
    }
}

type BoxError = Box<dyn std::error::Error + Send + Sync>;

/// Tower service that attaches SSH certificate auth tokens to requests.
#[derive(Clone)]
pub struct AuthService<S> {
    inner: S,
    state: Arc<Mutex<AuthLayerState>>,
}

impl<S, B> Service<http::Request<B>> for AuthService<S>
where
    S: Service<http::Request<B>> + Clone + Send + 'static,
    S::Future: Send,
    S::Response: Send,
    S::Error: Into<BoxError> + Send,
    B: Send + 'static,
{
    type Response = S::Response;
    type Error = BoxError;
    type Future = Pin<Box<dyn Future<Output = Result<Self::Response, Self::Error>> + Send>>;

    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(cx).map_err(Into::into)
    }

    fn call(&mut self, mut request: http::Request<B>) -> Self::Future {
        // Standard Tower pattern: clone and swap to get an owned service
        // for the async block.
        let clone = self.inner.clone();
        let mut inner = std::mem::replace(&mut self.inner, clone);
        let state = self.state.clone();

        Box::pin(async move {
            {
                let mut guard = state.lock().await;

                if let AuthLayerState::SshCert { agent, cert_blob } = &mut *guard {
                    let method = request.uri().path().to_string();
                    let header = Token::build_header_value(cert_blob, &method, agent)
                        .await
                        .map_err(|e| -> BoxError { Box::new(e) })?;

                    let value = header
                        .parse()
                        .map_err(|e: http::header::InvalidHeaderValue| -> BoxError { Box::new(e) })?;
                    request.headers_mut().insert("x-yanet-authentication", value);
                }
            }

            inner.call(request).await.map_err(Into::into)
        })
    }
}
