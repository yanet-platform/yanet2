// A configuration body with a field type that is not a shared-memory
// layout type must not derive ShmLayout.
#![forbid(unsafe_code)]

#[derive(yanet_sdk::ShmLayout)]
#[repr(C)]
struct Body {
    count: u64,
    flag: bool,
}

fn main() {}
