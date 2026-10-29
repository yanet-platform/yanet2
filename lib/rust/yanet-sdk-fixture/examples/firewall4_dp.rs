#![no_std]
#![no_main]
#![forbid(unsafe_code)]
use yanet_sdk as _;
yanet_sdk_plugin::export_module!(name = firewall4, module = yanet_sdk_fixture::firewall4::Firewall4);
