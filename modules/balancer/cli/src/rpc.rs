#[allow(non_snake_case)]
pub mod commonpb {
    tonic::include_proto!("commonpb");
}

#[allow(non_snake_case)]
pub mod balancerpb {
    tonic::include_proto!("balancerpb");
}

pub use balancerpb::balancer_service_client::BalancerServiceClient;
