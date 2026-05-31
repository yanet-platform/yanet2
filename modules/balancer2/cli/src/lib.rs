#[allow(clippy::all, non_snake_case)]
pub mod balancerpb {
    tonic::include_proto!("modules.balancer2.controlplane.balancerpb.v1");
}

pub use balancerpb::balancer_client::BalancerClient;
