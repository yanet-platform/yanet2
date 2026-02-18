//! SSH certificate authentication token builder.
//!
//! Produces tokens compatible with the server-side `sshcert` authenticator
//! defined in `controlplane/internal/auth/sshcert/token.go`.

use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use serde::Serialize;

use super::agent::{AgentError, SshAgent};

/// Token format version (must match server's `tokenVersion`).
const TOKEN_VERSION: i32 = 1;

/// Header prefix for the `x-yanet-authentication` metadata value.
const TOKEN_PREFIX: &str = "sshcert ";

/// SSH certificate authentication token.
///
/// JSON-serialized and base64-encoded into the `x-yanet-authentication`
/// gRPC metadata header.
#[derive(Debug, Serialize)]
pub struct Token {
    pub version: i32,
    pub certificate: String,
    pub timestamp: i64,
    pub nonce: String,
    pub method: String,
    pub signature: String,
}

impl Token {
    /// Build a signed token for the given gRPC method.
    ///
    /// Steps:
    /// 1. Fill in version, certificate (base64), timestamp (nanos), nonce.
    /// 2. Build canonical signed data.
    /// 3. Ask SSH agent to sign it.
    /// 4. Base64-encode the SSH wire-format signature.
    pub fn build(cert_blob: &[u8], method: &str, agent: &mut SshAgent) -> Result<Self, AgentError> {
        let certificate = BASE64.encode(cert_blob);
        let timestamp = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .expect("system clock before UNIX epoch")
            .as_nanos() as i64;
        let nonce = uuid::Uuid::new_v4().to_string();

        let canonical = format!(
            "version={}\ncertificate={}\ntimestamp={}\nnonce={}\nmethod={}",
            TOKEN_VERSION, certificate, timestamp, nonce, method,
        );

        let sig_bytes = agent.sign(cert_blob, canonical.as_bytes(), 0)?;
        let signature = BASE64.encode(&sig_bytes);

        Ok(Self {
            version: TOKEN_VERSION,
            certificate,
            timestamp,
            nonce,
            method: method.to_string(),
            signature,
        })
    }

    /// Encode the token into the `x-yanet-authentication` header value.
    ///
    /// Format: `sshcert <base64(json)>`.
    pub fn to_header_value(&self) -> Result<String, serde_json::Error> {
        let json = serde_json::to_string(self)?;
        let encoded = BASE64.encode(json.as_bytes());
        Ok(format!("{}{}", TOKEN_PREFIX, encoded))
    }
}
