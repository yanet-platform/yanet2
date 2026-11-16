//! The stash module as a plugin the dataplane loader picks up by its file name.

#![no_std]

yanet_sdk_plugin::export_module!(name = stash, module = yanet_sdk_fixture::Stash);
