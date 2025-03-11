use core::{
    error::Error,
    fmt::{self, Display, Formatter},
};

use prost::Message;

pub mod neighbour;

/// Custom error type for gRPC HTTP client
#[derive(Debug)]
pub enum GrpcHttpError {
    /// Request error
    Request(reqwest::Error),
    /// Server returned an error
    Server { status: u16, message: String },
    /// Error encoding the request
    Encode(String),
    /// Error decoding the response
    Decode(String),
}

impl Display for GrpcHttpError {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        match self {
            Self::Request(err) => write!(f, "HTTP request failed: {}", err),
            Self::Server { status, message } => {
                write!(f, "Server error ({}): {}", status, message)
            }
            Self::Encode(err) => write!(f, "Failed to encode request: {}", err),
            Self::Decode(err) => write!(f, "Failed to decode response: {}", err),
        }
    }
}

impl Error for GrpcHttpError {}

impl From<reqwest::Error> for GrpcHttpError {
    fn from(err: reqwest::Error) -> Self {
        GrpcHttpError::Request(err)
    }
}

/// A client for making HTTP requests to gRPC services
pub struct GrpcHttpClient {
    /// Base URL for the HTTP-gRPC gateway
    base_url: String,
    /// HTTP client for making requests
    client: reqwest::Client,
}

impl GrpcHttpClient {
    /// Create a new HTTP-gRPC client
    ///
    /// The base_url should be the URL of the HTTP-gRPC gateway, e.g. "http://localhost:8080"
    pub fn new(base_url: String) -> Self {
        Self {
            base_url,
            client: reqwest::Client::new(),
        }
    }

    /// Call a gRPC method over HTTP
    ///
    /// This method sends a binary protobuf message to the HTTP-gRPC gateway and
    /// expects a binary protobuf response.
    pub async fn call<Req, Resp>(&self, service: &str, method: &str, request: &Req) -> Result<Resp, GrpcHttpError>
    where
        Req: Message,
        Resp: Message + Default,
    {
        // Serialize the request message to bytes
        let mut buf = Vec::new();
        request
            .encode(&mut buf)
            .map_err(|e| GrpcHttpError::Encode(e.to_string()))?;

        // Format the URL
        let url = format!("{}/api/{}/{}", self.base_url, service, method);

        // Send the request
        let response = self
            .client
            .post(&url)
            .header("Content-Type", "application/x-protobuf")
            .body(buf)
            .send()
            .await?;

        // Check for error status
        if !response.status().is_success() {
            let status = response.status().as_u16();
            let message = response.text().await.unwrap_or_else(|_| String::from("Unknown error"));
            return Err(GrpcHttpError::Server { status, message });
        }

        // Get the binary response body
        let bytes = response.bytes().await?;

        // Deserialize the response
        let mut resp = Resp::default();
        resp.merge(bytes.as_ref())
            .map_err(|e| GrpcHttpError::Decode(e.to_string()))?;

        Ok(resp)
    }
}
