// The export macro accepts identifier paths only: a block cannot smuggle
// code into the macro expansion.
#![forbid(unsafe_code)]

use yanet_sdk::{Lpm4, Module, ShmLayout, register_module};

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

register_module! {
    name: decap,
    module: Decap,
    handler: { |_front, _config| {} },
}

fn main() {}
