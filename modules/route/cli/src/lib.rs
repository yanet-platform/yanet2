#[allow(clippy::all, clippy::std_instead_of_core, non_snake_case)]
pub mod routepb {
    tonic::include_proto!("modules.route.controlplane.routepb.v1");
}

pub mod fib;
