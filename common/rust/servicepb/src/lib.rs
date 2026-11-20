//! Prebuilt messages and clients for every in-tree module and operator
//! service proto.
//!
//! A CLI that calls an in-tree module's or operator's gRPC surface
//! depends on this crate instead of compiling its own copy of those
//! protos; the shared common, filter, and readiness types map onto their
//! own crates, so one compiled definition exists per message. The package
//! layout mirrors the proto packages: `pb::modules::<name>::controlplane::
//! <name>pb::v1` and `pb::operators::<name>::<name>pb::v1`.

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod pb {
    include!(concat!(env!("OUT_DIR"), "/mod.rs"));
}

#[cfg(test)]
mod tests {
    use super::pb;

    // Pins the prebuilt package layout, one referenced item per subtree:
    // every consumer path grows below these roots.
    #[test]
    fn packages_are_prebuilt() {
        let message = Option::<pb::modules::route::controlplane::routepb::v1::ListConfigsRequest>::None;
        let client = Option::<
            pb::operators::pipeline::operatorpb::v1::pipeline_operator_service_client::PipelineOperatorServiceClient<
                ync_rpc::Channel,
            >,
        >::None;
        let _ = (message, client);
    }
}
