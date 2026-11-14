use core::error::Error;

// Every in-tree module and operator service proto, prebuilt once so a
// consumer shares one compiled copy of the messages and clients instead
// of generating a private one.
const PROTOS: &[&str] = &[
    "modules/acl/controlplane/aclpb/v1/acl.proto",
    "modules/blackhole/controlplane/blackholepb/v1/blackhole.proto",
    "modules/decap/controlplane/decappb/v1/decap.proto",
    "modules/dscp/controlplane/dscppb/v1/dscp.proto",
    "modules/forward/controlplane/forwardpb/v1/forward.proto",
    "modules/fwstate/controlplane/fwstatepb/v1/fwstate.proto",
    "modules/l3b/controlplane/l3bpb/v1/l3b.proto",
    "modules/mirror/controlplane/mirrorpb/v1/mirror.proto",
    "modules/nat64/controlplane/nat64pb/v1/nat64.proto",
    "modules/pdump/controlplane/pdumppb/v1/pdump.proto",
    "modules/route/controlplane/routepb/v1/route.proto",
    "modules/route-mpls/controlplane/routemplspb/v1/routempls.proto",
    "modules/unrdup/controlplane/unrduppb/v1/unrdup.proto",
    "operators/bird-adapter/adapterpb/v1/bird_import.proto",
    "operators/pipeline/operatorpb/v1/operator.proto",
    "operators/pipeline/operatorpb/v1/readiness.proto",
    "operators/route/operatorpb/v1/neighbour.proto",
    "operators/route/operatorpb/v1/operator.proto",
    "operators/route/operatorpb/v1/readiness.proto",
    "operators/route/operatorpb/v1/route.proto",
];

fn main() -> Result<(), Box<dyn Error>> {
    for proto in PROTOS {
        println!("cargo:rerun-if-changed=../../../{proto}");
    }

    ync_build::client("../../..", PROTOS)
        .runtime("::ync_rpc")
        .serialize()
        .with(|builder| {
            builder
                // Oneofs generate their own enums, which the blanket
                // message derive above does not cover.
                .enum_attribute(".", "#[derive(serde::Serialize)]")
                // The readiness messages are prebuilt in their own crate;
                // the operator protos importing them map onto it.
                .extern_path(".common.readinesspb.v1", "::readinesspb::pb")
                // One include file for every package: the package trees
                // share top-level segments, so per-package includes would
                // collide.
                .include_file("mod.rs")
        })
        .compile()
}
