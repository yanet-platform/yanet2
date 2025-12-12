//! gRPC proto module definitions

#[allow(non_snake_case, dead_code)]
pub mod commonpb {
    tonic::include_proto!("commonpb");
}

#[allow(non_snake_case)]
pub mod balancerpb {
    tonic::include_proto!("balancerpb");
}

pub use balancerpb::balancer_service_client::BalancerServiceClient;