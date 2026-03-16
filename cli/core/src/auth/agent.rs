//! Asynchronous SSH Agent client over Unix domain socket.
//!
//! Implements only the subset of the SSH agent protocol needed for
//! authentication: listing identities and signing data.

use std::{env, io, path::Path};

use byteorder::{BigEndian, ByteOrder, WriteBytesExt};
use ssh_key::{Algorithm, Certificate, PublicKey};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::UnixStream,
};

const SSH_AGENT_FAILURE: u8 = 5;
const SSH_AGENTC_REQUEST_IDENTITIES: u8 = 11;
const SSH_AGENT_IDENTITIES_ANSWER: u8 = 12;
const SSH_AGENTC_SIGN_REQUEST: u8 = 13;
const SSH_AGENT_SIGN_RESPONSE: u8 = 14;

/// Maximum allowed agent response size (1 MiB).
const MAX_RESPONSE_SIZE: usize = 1 << 20;

/// Maximum number of identities accepted from agent.
const MAX_IDENTITIES: usize = 1024;

#[derive(Debug, thiserror::Error)]
pub enum AgentError {
    #[error("SSH_AUTH_SOCK environment variable not set")]
    NoAuthSock,

    #[error("I/O error: {0}")]
    Io(#[from] io::Error),

    #[error("SSH agent returned failure")]
    AgentFailure,

    #[error("unexpected response type: expected {expected}, got {got}")]
    UnexpectedResponse { expected: u8, got: u8 },

    #[error("invalid agent response: {0}")]
    InvalidResponse(String),

    #[error("SSH key parse error: {0}")]
    SshKey(#[from] ssh_key::Error),

    #[error("no suitable certificate found in SSH agent")]
    NoCertificateFound,

    #[error("payload too large for SSH agent protocol")]
    PayloadTooLarge,
}

#[derive(Debug)]
pub enum SshIdentity {
    PublicKey(PublicKey),
    Certificate(Box<Certificate>),
}

impl SshIdentity {
    /// Parse from raw wire bytes (RFC 4253 section 6.6 format).
    fn from_wire_bytes(buf: &[u8]) -> Result<Self, AgentError> {
        // Read the algorithm string to determine if it's a certificate.
        let id = read_wire_string(buf).ok_or_else(|| AgentError::InvalidResponse("truncated key blob".into()))?;

        if Algorithm::new_certificate(id).is_ok() {
            let cert = Certificate::from_bytes(buf)?;
            Ok(Self::Certificate(Box::new(cert)))
        } else {
            let pk = PublicKey::from_bytes(buf)?;
            Ok(Self::PublicKey(pk))
        }
    }

    /// Returns the certificate if this identity is one.
    pub fn certificate(&self) -> Option<&Certificate> {
        match self {
            Self::PublicKey(..) => None,
            Self::Certificate(c) => Some(c.as_ref()),
        }
    }

    /// Returns the raw wire bytes of this identity.
    pub fn to_bytes(&self) -> Result<Vec<u8>, AgentError> {
        match self {
            Self::PublicKey(pk) => Ok(pk.to_bytes()?),
            Self::Certificate(cert) => Ok(cert.to_bytes()?),
        }
    }
}

#[derive(Debug)]
pub struct SshAgent {
    sock: UnixStream,
}

impl SshAgent {
    /// Connect to the SSH agent using `SSH_AUTH_SOCK` environment variable.
    pub async fn from_env() -> Result<Self, AgentError> {
        let path = env::var("SSH_AUTH_SOCK").map_err(|_| AgentError::NoAuthSock)?;
        Self::connect(&path).await
    }

    /// Connect to the SSH agent at the given socket path.
    pub async fn connect<P: AsRef<Path>>(path: P) -> Result<Self, AgentError> {
        let sock = UnixStream::connect(path).await?;
        Ok(Self { sock })
    }

    /// List all identities from the SSH agent.
    pub async fn identities(&mut self) -> Result<Vec<SshIdentity>, AgentError> {
        self.send_message(SSH_AGENTC_REQUEST_IDENTITIES, &[]).await?;
        let response = self.recv_message(SSH_AGENT_IDENTITIES_ANSWER).await?;
        parse_identities_answer(&response)
    }

    /// Find the first certificate matching the given tag.
    pub async fn find_certificate(&mut self, tag: &str) -> Result<(Certificate, Vec<u8>), AgentError> {
        let identities = self.identities().await?;

        for identity in identities {
            if let Some(cert) = identity.certificate() {
                if cert.key_id().contains(tag) {
                    let blob = identity.to_bytes()?;

                    if let SshIdentity::Certificate(cert) = identity {
                        return Ok((*cert, blob));
                    }
                }
            }
        }

        Err(AgentError::NoCertificateFound)
    }

    /// Sign data using the key identified by `key_blob`.
    ///
    /// Returns the full SSH wire-format signature, suitable for base64-encoding
    /// into the token.
    pub async fn sign(&mut self, key_blob: &[u8], data: &[u8], flags: u32) -> Result<Vec<u8>, AgentError> {
        let mut payload = Vec::new();
        write_wire_bytes(&mut payload, key_blob)?;
        write_wire_bytes(&mut payload, data)?;
        WriteBytesExt::write_u32::<BigEndian>(&mut payload, flags)?;

        self.send_message(SSH_AGENTC_SIGN_REQUEST, &payload).await?;

        let response = self.recv_message(SSH_AGENT_SIGN_RESPONSE).await?;
        parse_sign_response(&response).map(|s| s.to_vec())
    }

    /// Send a framed message to the agent.
    async fn send_message(&mut self, ty: u8, payload: &[u8]) -> Result<(), AgentError> {
        let len = u32::try_from(1 + payload.len()).map_err(|_| AgentError::PayloadTooLarge)?;
        self.sock.write_u32(len).await?;
        self.sock.write_u8(ty).await?;
        self.sock.write_all(payload).await?;
        Ok(())
    }

    /// Receive a framed message from the agent.
    async fn recv_message(&mut self, expected_type: u8) -> Result<Vec<u8>, AgentError> {
        let mut len_buf = [0u8; 4];
        self.sock.read_exact(&mut len_buf).await?;
        let len = BigEndian::read_u32(&len_buf) as usize;

        if len == 0 {
            return Err(AgentError::InvalidResponse("zero-length response".into()));
        }
        if len > MAX_RESPONSE_SIZE {
            return Err(AgentError::InvalidResponse(format!(
                "response too large: {len} > {MAX_RESPONSE_SIZE}"
            )));
        }

        let mut buf = vec![0u8; len];
        self.sock.read_exact(&mut buf).await?;

        let ty = buf[0];
        if ty == SSH_AGENT_FAILURE {
            return Err(AgentError::AgentFailure);
        }
        if ty != expected_type {
            return Err(AgentError::UnexpectedResponse { expected: expected_type, got: ty });
        }

        Ok(buf[1..].to_vec())
    }
}

/// Read a length-prefixed string from wire bytes, returning the string
/// content.
fn read_wire_string(buf: &[u8]) -> Option<&str> {
    if buf.len() < 4 {
        return None;
    }

    let len = BigEndian::read_u32(buf) as usize;
    if buf.len() < 4 + len {
        return None;
    }

    core::str::from_utf8(&buf[4..4 + len]).ok()
}

/// Read a length-prefixed byte string, returning (bytes, remaining).
fn read_wire_bytes(buf: &[u8]) -> Option<(&[u8], &[u8])> {
    if buf.len() < 4 {
        return None;
    }

    let len = BigEndian::read_u32(buf) as usize;
    if buf.len() < 4 + len {
        return None;
    }

    Some((&buf[4..4 + len], &buf[4 + len..]))
}

/// Write a length-prefixed byte string.
fn write_wire_bytes<W: io::Write>(w: &mut W, data: &[u8]) -> Result<(), AgentError> {
    let len = u32::try_from(data.len()).map_err(|_| AgentError::PayloadTooLarge)?;
    w.write_u32::<BigEndian>(len)?;
    w.write_all(data)?;

    Ok(())
}

/// Parse SSH_AGENT_IDENTITIES_ANSWER payload.
fn parse_identities_answer(buf: &[u8]) -> Result<Vec<SshIdentity>, AgentError> {
    if buf.len() < 4 {
        return Err(AgentError::InvalidResponse("truncated identities answer".into()));
    }

    let num_keys = BigEndian::read_u32(buf) as usize;
    if num_keys > MAX_IDENTITIES {
        return Err(AgentError::InvalidResponse(format!(
            "too many identities: {num_keys} > {MAX_IDENTITIES}"
        )));
    }

    let mut rest = &buf[4..];
    let mut identities = Vec::with_capacity(num_keys);

    for _ in 0..num_keys {
        let (key_blob, remaining) =
            read_wire_bytes(rest).ok_or_else(|| AgentError::InvalidResponse("truncated key blob".into()))?;

        let identity = SshIdentity::from_wire_bytes(key_blob)?;

        // Skip comment.
        let (.., remaining) =
            read_wire_bytes(remaining).ok_or_else(|| AgentError::InvalidResponse("truncated comment".into()))?;

        identities.push(identity);
        rest = remaining;
    }

    Ok(identities)
}

/// Parse SSH_AGENT_SIGN_RESPONSE payload.
///
/// Returns the full SSH wire-format signature (the inner string containing
/// algorithm + blob), which is what the Go server expects for
/// `ssh.Unmarshal(sigBytes, &sig)`.
fn parse_sign_response(buf: &[u8]) -> Result<&[u8], AgentError> {
    // The response is a single string containing the signature.
    let (signature, ..) =
        read_wire_bytes(buf).ok_or_else(|| AgentError::InvalidResponse("truncated sign response".into()))?;

    Ok(signature)
}
