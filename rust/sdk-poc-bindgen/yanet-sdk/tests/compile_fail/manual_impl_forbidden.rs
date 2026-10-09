// A module crate cannot implement the layout trait by hand: the impl is
// unsafe and the crate forbids unsafe code.
#![forbid(unsafe_code)]

use yanet_sdk::shm::{ShmLayout, ValidationError, Validator};

#[repr(C)]
struct Body {
    flag: bool,
}

unsafe impl ShmLayout for Body {
    const FINGERPRINT: u64 = 0;

    fn validate(_: &Validator<'_>, _: usize) -> Result<(), ValidationError> {
        Ok(())
    }
}

fn main() {}
