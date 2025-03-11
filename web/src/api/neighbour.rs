use core::fmt;

use self::code::{ListNeighboursRequest, ListNeighboursResponse, NeighbourEntry};
use super::{GrpcHttpClient, GrpcHttpError};

#[allow(non_snake_case)]
mod code {
    include!(concat!(env!("OUT_DIR"), "/routepb.rs"));
}

/// Client for interacting with the Neighbour API.
pub struct NeighbourClient {
    client: GrpcHttpClient,
}

impl NeighbourClient {
    /// Constructs a new [`NeighbourClient`].
    pub fn new(base_url: String) -> Self {
        Self {
            client: GrpcHttpClient::new(base_url),
        }
    }

    pub async fn list_neighbours(&self) -> Result<Vec<NeighbourEntry>, GrpcHttpError> {
        let request = ListNeighboursRequest {};

        let response = self
            .client
            .call::<ListNeighboursRequest, ListNeighboursResponse>("routepb.Neighbour", "List", &request)
            .await?;

        Ok(response.neighbours)
    }
}
