//! Packet dump mode configuration and utilities.
//!
//! This module provides types and functions for configuring packet capture
//! modes, including input packets and dropped packets

use core::ops::BitOrAssign;

use clap::Args;

/// The dataplane's capture queue bits, read from its header at build time.
///
/// They are carried unchanged in the config mode and in every record's
/// queue.
mod mode {
    include!(concat!(env!("OUT_DIR"), "/pdump_mode.rs"));
}

/// Capture input packets mode flag
pub const INPUT: u32 = mode::PDUMP_INPUT;
/// Capture dropped packets mode flag
pub const DROPS: u32 = mode::PDUMP_DROPS;
/// Capture all packets (input, drops) mode flag
pub const ALL: u32 = mode::PDUMP_ALL;

#[derive(Args, Debug, Clone, Copy)]
#[group(required = false, multiple = true)]
pub struct Mode {
    /// Capture input packets.
    #[arg(long)]
    pub input: bool,

    /// Capture dropped packets.
    #[arg(long)]
    pub drops: bool,

    /// Capture all packets (input and drops).
    #[arg(long)]
    pub all: bool,
}

pub fn to_str(mode: u32) -> &'static str {
    let input = mode & INPUT != 0;
    let drops = mode & DROPS != 0;

    // This match covers all combinations to avoid allocations
    match (input, drops) {
        (false, false) => "UNKNOWN",
        (false, true) => "DROPS",
        (true, false) => "INPUT",
        (true, true) => "INPUT|DROPS",
    }
}

pub fn to_char(mode: u32) -> char {
    let input = mode & INPUT != 0;
    let drops = mode & DROPS != 0;
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
            mode.bitor_assign(INPUT);
        }
        if val.drops {
            mode.bitor_assign(DROPS);
        }
        if val.all {
            mode.bitor_assign(ALL);
        }
        mode
    }
}
