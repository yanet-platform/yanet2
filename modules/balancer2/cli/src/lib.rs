#[allow(clippy::all, non_snake_case)]
pub mod balancerpb {
    tonic::include_proto!("balancerpb");
}

pub use balancerpb::balancer_client::BalancerClient;
