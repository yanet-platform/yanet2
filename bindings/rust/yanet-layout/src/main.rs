//! Writes the Go views of the fixture modules' configurations into
//! `controlplane/native/sdk/sdkfixture`.

use std::process::ExitCode;

use yanet_layout::Constant;
use yanet_sdk::{Object, ShmLayout};
use yanet_sdk_fixture::{
    Config, FIXTURE_MODE_ALLOC_PREPEND, FIXTURE_MODE_CLONE, FIXTURE_MODE_EMIT, FIXTURE_MODE_FORGET,
    FIXTURE_MODE_FORWARD, FIXTURE_MODE_FREE, FIXTURE_MODE_PARITY, FIXTURE_MODE_PASS, FIXTURE_MODE_REENTER,
    FIXTURE_MODE_RESOLVE, FIXTURE_MODE_RESOLVE_INPUT, FIXTURE_MODE_SHUFFLE, FIXTURE_MODE_WORKER, RouteFib, StashConfig,
    State, StateConfig, TallyConfig, assign::AssignConfig, hosts::HostsConfig,
};

fn main() -> ExitCode {
    let generated = yanet_layout::generate(
        concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../../controlplane/native/sdk/sdkfixture"
        )
        .as_ref(),
        "github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture",
        &[
            AssignConfig::LAYOUT,
            Config::LAYOUT,
            RouteFib::LAYOUT,
            StashConfig::LAYOUT,
            StateConfig::LAYOUT,
            TallyConfig::LAYOUT,
            HostsConfig::LAYOUT,
        ],
        &[
            ("STATE_TYPE", Constant::Str(State::TYPE)),
            ("FIXTURE_MODE_PASS", Constant::U64(FIXTURE_MODE_PASS)),
            ("FIXTURE_MODE_FORGET", Constant::U64(FIXTURE_MODE_FORGET)),
            ("FIXTURE_MODE_SHUFFLE", Constant::U64(FIXTURE_MODE_SHUFFLE)),
            ("FIXTURE_MODE_PARITY", Constant::U64(FIXTURE_MODE_PARITY)),
            ("FIXTURE_MODE_WORKER", Constant::U64(FIXTURE_MODE_WORKER)),
            ("FIXTURE_MODE_FORWARD", Constant::U64(FIXTURE_MODE_FORWARD)),
            ("FIXTURE_MODE_RESOLVE", Constant::U64(FIXTURE_MODE_RESOLVE)),
            ("FIXTURE_MODE_RESOLVE_INPUT", Constant::U64(FIXTURE_MODE_RESOLVE_INPUT)),
            ("FIXTURE_MODE_REENTER", Constant::U64(FIXTURE_MODE_REENTER)),
            ("FIXTURE_MODE_EMIT", Constant::U64(FIXTURE_MODE_EMIT)),
            ("FIXTURE_MODE_CLONE", Constant::U64(FIXTURE_MODE_CLONE)),
            ("FIXTURE_MODE_ALLOC_PREPEND", Constant::U64(FIXTURE_MODE_ALLOC_PREPEND)),
            ("FIXTURE_MODE_FREE", Constant::U64(FIXTURE_MODE_FREE)),
        ],
    );
    match generated {
        Ok(()) => ExitCode::SUCCESS,
        Err(err) => {
            eprintln!("yanet-layout: {err}");
            ExitCode::FAILURE
        }
    }
}
