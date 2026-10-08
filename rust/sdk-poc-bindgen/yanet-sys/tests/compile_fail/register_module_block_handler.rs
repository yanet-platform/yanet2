// The export macro accepts identifier paths only: a block cannot smuggle
// code into the macro expansion.
#![forbid(unsafe_code)]

yanet_sys::register_module! {
    name: decap,
    config: yanet_sys::module::Decap,
    handler: { |_front, _config| {} },
}

fn main() {}
