use leptos::prelude::window;

use self::code::{ListNeighboursRequest, ListNeighboursResponse, NeighbourEntry};
use super::{GrpcHttpClient, GrpcHttpError};

#[allow(non_snake_case)]
pub mod code {
    include!(concat!(env!("OUT_DIR"), "/routepb.rs"));
}

/// Client for interacting with the Neighbour API.
pub struct NeighbourClient {
    client: GrpcHttpClient,
}

impl NeighbourClient {
    pub fn new() -> Self {
        Self {
            client: GrpcHttpClient::new(window().origin()),
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
