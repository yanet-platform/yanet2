//! The stash module with the state object type it links, in one plugin.
//!
//! Its file name is outside the loader's `lib<name>_dp.so` pattern, so the
//! plugin directory of the fixtures leaves it alone, where the plugins of the
//! stash module and of the state type would collide with it; a test links it
//! into a directory of its own under a name the loader maps.

#![no_std]
// The example shares the dependencies of its package.
#![allow(unused_crate_dependencies)]

yanet_sdk_plugin::export_module!(
    name = stash,
    module = yanet_sdk_fixture::Stash,
    objects = [fixture_state = yanet_sdk_fixture::State],
);
