#[allow(clippy::all, non_snake_case)]
pub mod filterpb {
    tonic::include_proto!("filterpb");
}

#[allow(clippy::all, non_snake_case)]
pub mod commonpb {
    tonic::include_proto!("commonpb");
}

#[allow(clippy::all, non_snake_case)]
pub mod balancerpb {
    tonic::include_proto!("balancerpb");
}

pub use balancerpb::balancer_client::BalancerClient;
