//! A caller's core alias must not replace the unsafe writer's cleanup.

extern crate core as actual_core;
extern crate self as core;
extern crate self as yanet_sdk;
extern crate yanet_sdk as actual_sdk;

pub use actual_core::*;
pub mod mem {
    pub use actual_core::mem::*;

    pub fn forget<T>(value: T) {
        drop(value);
    }
}

use actual_sdk::{BuildError, boxed, build_config};
use yanet_sdk_testing::cp::TestOwner;

mod author {
    #![forbid(unsafe_code)]

    use actual_sdk::{CommitCx, prelude::*};

    #[derive(ShmLayout)]
    #[repr(C)]
    pub struct Config {
        pub child: ShmBox<u64>,
    }

    pub struct Mine;
    impl Module for Mine {
        const NAME: &'static str = "mine";
        type Config = Config;
        type Prepared<'p> = ();
        fn prepare(_: &CommitCx<'_, Self>) {}
        fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
            batch.dispatch(|_| Verdict::Pass);
        }
    }
}

#[test]
fn test_caller_cannot_turn_forget_into_drop() {
    // SAFETY: this configuration contains no embedded C object.
    let owner = unsafe { TestOwner::new() };
    let built = build_config::<author::Mine, _, BuildError<yanet_sdk_testing::cp::TestError>>(
        &owner,
        "mine",
        author::ConfigInit { child: boxed(7u64) },
    )
    .unwrap();
    assert_eq!(2, owner.live_blocks(), "the child must remain owned by the root");
    // SAFETY: this is the owner that built the graph, with no live references.
    unsafe { built.destroy(&owner) };
    owner.assert_released();
}

use yanet_packet as _;
use yanet_sdk_derive as _;
use yanet_shm as _;
use yanet_sys as _;
