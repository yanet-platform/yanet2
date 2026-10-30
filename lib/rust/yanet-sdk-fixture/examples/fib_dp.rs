//! The fib module as a plugin the dataplane loader picks up by its file name.

#![no_std]
// The example shares the dependencies of its package.
#![allow(unused_crate_dependencies)]

yanet_sdk_plugin::export_module!(name = fib, module = yanet_sdk_fixture::Fib);
