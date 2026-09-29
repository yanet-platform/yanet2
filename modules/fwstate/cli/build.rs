use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/fwstate/controlplane/fwstatepb/v1/fwstate.proto"])
        .with(|builder| builder.protoc_arg("--experimental_allow_proto3_optional"))
        .serialize()
        .compile()
}
