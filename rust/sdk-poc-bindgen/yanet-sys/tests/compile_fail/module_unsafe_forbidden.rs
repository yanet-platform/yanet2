// A module crate cannot write unsafe code, even next to the export macro.
#![forbid(unsafe_code)]

fn handle(
    _front: &mut yanet_sys::packet::PacketFront<'_>,
    _config: &yanet_sys::views::DecapConfig<'_, yanet_sys::rel::MapResolver<'_>>,
) {
    let value = 0u8;
    let _ = unsafe { core::ptr::read(&value) };
}

yanet_sys::register_module! {
    name: decap,
    config: yanet_sys::module::Decap,
    handler: handle,
}

fn main() {}
