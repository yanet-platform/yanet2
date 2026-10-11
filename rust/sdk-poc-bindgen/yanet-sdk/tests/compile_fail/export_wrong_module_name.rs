// A module cannot be exported under another name than its Module::NAME.
#![forbid(unsafe_code)]

use yanet_sdk::{Config, Lpm4, Module, PacketFront, ShmLayout, register_module};

#[derive(ShmLayout)]
#[repr(C)]
pub struct DecapConfig {
    pub prefixes4: Lpm4,
}

pub struct Decap;

impl Module for Decap {
    const NAME: &'static str = "decap";
    type Config = DecapConfig;
}

fn handle(_front: &mut PacketFront<'_>, _config: Config<'_, DecapConfig>) {}

register_module! {
    name: dscp,
    module: Decap,
    handler: handle,
}

fn main() {}
