#[allow(clippy::all, non_snake_case)]
pub mod pb {
    tonic::include_proto!("common.filterpb.v1");
}

pub mod network;
