//! Packet dump mode configuration and utilities.
//!
//! This module provides types and functions for configuring packet capture
//! modes, including input packets and dropped packets

use std::ops::BitOrAssign;

use clap::Args;

#[allow(non_upper_case_globals)]
#[allow(non_camel_case_types)]
#[allow(non_snake_case)]
mod mode {
    // Auto-generated bindings from C header file
    include!(concat!(env!("OUT_DIR"), "/pdump_mode.rs"));
}

pub use mode::pdump_mode;

#[allow(dead_code)]
/// Capture input packets mode flag
pub const INPUT: pdump_mode = mode::pdump_mode_PDUMP_INPUT;
#[allow(dead_code)]
/// Capture dropped packets mode flag
pub const DROPS: pdump_mode = mode::pdump_mode_PDUMP_DROPS;
#[allow(dead_code)]
/// Capture all packets (input, drops) mode flag
pub const ALL: pdump_mode = mode::pdump_mode_PDUMP_ALL;

#[derive(Args, Debug, Clone, Copy)]
#[group(required = false, multiple = true)]
pub struct Mode {
    /// Capture input packets
    #[arg(long)]
    pub input: bool,

    /// Capture dropped packets
    #[arg(long)]
    pub drops: bool,

    /// Capture all packets (input and drops)
    #[arg(long)]
    pub all: bool,
}

pub fn to_str(mode: pdump_mode) -> &'static str {
    let input = mode & mode::pdump_mode_PDUMP_INPUT != 0;
    let drops = mode & mode::pdump_mode_PDUMP_DROPS != 0;

    // This match covers all combinations to avoid allocations
    match (input, drops) {
        (false, false) => "UNKNOWN",
        (false, true) => "DROPS",
        (true, false) => "INPUT",
        (true, true) => "INPUT|DROPS",
    }
}

pub fn to_char(mode: pdump_mode) -> char {
    let input = mode & mode::pdump_mode_PDUMP_INPUT != 0;
    let drops = mode & mode::pdump_mode_PDUMP_DROPS != 0;
    match (input, drops) {
        (true, false) => 'I',
        (false, true) => 'D',
        _ => 'U', // Multiple modes active or unknown mode, return 'U' for 'UNKNOWN'
    }
}

impl From<Mode> for u32 {
    fn from(val: Mode) -> Self {
        let mut mode: u32 = 0;
        if val.input {
            mode.bitor_assign(mode::pdump_mode_PDUMP_INPUT);
        }
        if val.drops {
            mode.bitor_assign(mode::pdump_mode_PDUMP_DROPS);
        }
        if val.all {
            mode.bitor_assign(mode::pdump_mode_PDUMP_ALL);
        }
        mode
    }
}
