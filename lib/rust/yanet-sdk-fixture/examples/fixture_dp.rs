//! The fixture module as a plugin the dataplane loader picks up by its file
//! name.

#![no_std]
#![forbid(unsafe_code)]

use yanet_sdk as _;

yanet_sdk_plugin::export_module!(name = fixture, module = yanet_sdk_fixture::Fixture);
