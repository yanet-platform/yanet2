#[allow(non_snake_case)]
pub mod commonpb {
    tonic::include_proto!("commonpb");
}

#[allow(non_snake_case)]
pub mod aclpb {
    tonic::include_proto!("aclpb");
}

pub use aclpb::acl_service_client::AclServiceClient;
