//! Packet dump mode configuration and utilities.
//!
//! This module provides types and functions for configuring packet capture modes,
//! including input packets, dropped packets, and bypassed packets.

use clap::Args;
use std::ops::BitOrAssign;

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
/// Capture bypassed packets mode flag
pub const BYPASS: pdump_mode = mode::pdump_mode_PDUMP_BYPASS;
#[allow(dead_code)]
/// Capture all packets (input, drops, and bypass) mode flag
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

    /// Capture bypassed packets
    #[arg(long)]
    pub bypass: bool,

    /// Capture all packets (input, drops, and bypass)
    #[arg(long)]
    pub all: bool,
}

pub fn to_str(mode: pdump_mode) -> &'static str {
    let input = mode & mode::pdump_mode_PDUMP_INPUT != 0;
    let drops = mode & mode::pdump_mode_PDUMP_DROPS != 0;
    let bypass = mode & mode::pdump_mode_PDUMP_BYPASS != 0;

    // This match covers all combinations to avoid allocations
    match (input, drops, bypass) {
        (false, false, false) => "UNKNOWN",
        (false, false, true) => "BYPASS",
        (false, true, false) => "DROPS",
        (false, true, true) => "DROPS|BYPASS",
        (true, false, false) => "INPUT",
        (true, false, true) => "INPUT|BYPASS",
        (true, true, false) => "INPUT|DROPS",
        (true, true, true) => "INPUT|DROPS|BYPASS",
    }
}

pub fn to_char(mode: pdump_mode) -> char {
    let input = mode & mode::pdump_mode_PDUMP_INPUT != 0;
    let drops = mode & mode::pdump_mode_PDUMP_DROPS != 0;
    let bypass = mode & mode::pdump_mode_PDUMP_BYPASS != 0;
    match (input, drops, bypass) {
        (true, false, false) => 'I',
        (false, true, false) => 'D',
        (false, false, true) => 'B',
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
        if val.bypass {
            mode.bitor_assign(mode::pdump_mode_PDUMP_BYPASS);
        }
        if val.all {
            mode.bitor_assign(mode::pdump_mode_PDUMP_ALL);
        }
        mode
    }
}
