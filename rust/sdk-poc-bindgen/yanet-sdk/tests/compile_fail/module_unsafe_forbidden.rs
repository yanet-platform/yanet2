// A module crate cannot write unsafe code, even next to the export macro
// and the layout derive, whose own expansions the lint does not see.
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

fn handle(_front: &mut PacketFront<'_>, _config: Config<'_, DecapConfig>) {
    let value = 0u8;
    let _ = unsafe { core::ptr::read(&value) };
}

register_module! {
    name: decap,
    module: Decap,
    handler: handle,
}

fn main() {}
