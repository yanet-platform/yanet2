//! Tonic gRPC interceptor for SSH certificate authentication.
//!
//! Uses a tower [`Layer`]/[`Service`] to intercept HTTP/2 requests and
//! attach a signed SSH certificate token to the `x-yanet-authentication`
//! metadata header. This approach (vs tonic's `Interceptor` trait) gives
//! access to the request URI, which is needed for method binding.

use std::{
    sync::{Arc, Mutex},
    task::{Context, Poll},
};

use tower::{Layer, Service};

use super::{
    agent::{AgentError, SshAgent},
    token::Token,
};

/// Tower layer that wraps a service with SSH certificate authentication.
#[derive(Clone)]
pub struct AuthLayer {
    inner: Arc<Mutex<AuthLayerState>>,
}

enum AuthLayerState {
    SshCert { agent: SshAgent, cert_blob: Vec<u8> },
    Nop,
}

impl AuthLayer {
    /// Create an auth layer that signs requests using the SSH agent.
    pub fn sshcert(agent: SshAgent, cert_blob: Vec<u8>) -> Self {
        Self {
            inner: Arc::new(Mutex::new(AuthLayerState::SshCert { agent, cert_blob })),
        }
    }

    /// Create an auth layer from the SSH agent, finding a `:insecure:`
    /// certificate.
    pub fn from_agent() -> Result<Self, AgentError> {
        let mut agent = SshAgent::from_env()?;
        // TODO: configurable certificate tag.
        let (_cert, blob) = agent.find_certificate(":insecure:")?;
        Ok(Self::sshcert(agent, blob))
    }

    /// Create a no-op auth layer that passes requests through unchanged.
    pub fn nop() -> Self {
        Self {
            inner: Arc::new(Mutex::new(AuthLayerState::Nop)),
        }
    }
}

impl<S> Layer<S> for AuthLayer {
    type Service = AuthService<S>;

    fn layer(&self, inner: S) -> Self::Service {
        AuthService { inner, state: self.inner.clone() }
    }
}

/// Tower service that attaches SSH certificate auth tokens to requests.
#[derive(Clone)]
pub struct AuthService<S> {
    inner: S,
    state: Arc<Mutex<AuthLayerState>>,
}

impl<S, B> Service<http::Request<B>> for AuthService<S>
where
    S: Service<http::Request<B>>,
{
    type Response = S::Response;
    type Error = S::Error;
    type Future = S::Future;

    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(cx)
    }

    fn call(&mut self, mut request: http::Request<B>) -> Self::Future {
        let mut state = self.state.lock().expect("auth state lock poisoned");

        if let AuthLayerState::SshCert { agent, cert_blob } = &mut *state {
            let method = request.uri().path().to_string();

            match Token::build(cert_blob, &method, agent) {
                Ok(token) => match token.to_header_value() {
                    Ok(header_value) => {
                        if let Ok(value) = header_value.parse() {
                            request.headers_mut().insert("x-yanet-authentication", value);
                        }
                    }
                    Err(e) => {
                        log::error!("failed to serialize sshcert token: {e}");
                    }
                },
                Err(e) => {
                    log::error!("failed to build sshcert token: {e}");
                }
            }
        }

        drop(state);
        self.inner.call(request)
    }
}
