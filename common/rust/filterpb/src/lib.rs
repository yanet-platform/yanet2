#[allow(clippy::all, non_snake_case)]
pub mod pb {
    tonic::include_proto!("filterpb");
}

pub mod network;
