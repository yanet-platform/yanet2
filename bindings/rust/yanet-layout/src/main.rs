//! Writes the Go views of the fixture modules' configurations into
//! `controlplane/native/sdk/sdkfixture`.

use std::process::ExitCode;

use yanet_sdk::{Object, ShmLayout};
use yanet_sdk_fixture::{StashConfig, State, StateConfig, assign::AssignConfig};

fn main() -> ExitCode {
    let generated = yanet_layout::generate(
        concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../../controlplane/native/sdk/sdkfixture"
        )
        .as_ref(),
        "github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture",
        &[AssignConfig::LAYOUT, StashConfig::LAYOUT, StateConfig::LAYOUT],
        &[("STATE_TYPE", State::TYPE)],
    );
    match generated {
        Ok(()) => ExitCode::SUCCESS,
        Err(err) => {
            eprintln!("yanet-layout: {err}");
            ExitCode::FAILURE
        }
    }
}
