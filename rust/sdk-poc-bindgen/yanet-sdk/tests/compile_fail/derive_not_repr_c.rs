// A configuration body without repr(C) has no stable layout and must not
// derive ShmLayout.
#![forbid(unsafe_code)]

#[derive(yanet_sdk::ShmLayout)]
struct Body {
    count: u64,
}

fn main() {}
