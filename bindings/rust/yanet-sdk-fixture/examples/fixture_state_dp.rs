//! The state object type as a plugin the dataplane loads by its type name.

#![no_std]

yanet_sdk_plugin::export_object!(name = fixture_state, object = yanet_sdk_fixture::State);
