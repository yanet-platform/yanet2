//! example_rs: the Rust-dataplane twin of sdk/example.
//!
//! Every packet routed to the module is dropped, mirroring the C example
//! one-for-one so the two SDK frontends stay directly comparable. The
//! control-plane side is the same C `api/` library plus Go daemon every
//! module uses; only the handler is Rust. See docs/module-sdk.md and
//! docs/rust-dataplane-modules.md.

use yanet_dp::prelude::*;

/// The module's shared-memory config: a bare `cp_module`, so the mirror
/// is the crate's own `CpModule`.
#[repr(C)]
struct ExampleRsConfig {
    cp_module: yanet_shm::CpModule,
}

// SAFETY: cp_module is the first field, matching the C config.
unsafe impl ModuleConfig for ExampleRsConfig {
    fn cp_module(&self) -> &yanet_shm::CpModule {
        &self.cp_module
    }
}

fn handle(ectx: &mut Ectx<'_>, front: &mut PacketFront<'_>) {
    let _ = ectx.config::<ExampleRsConfig>();
    while let Some(packet) = front.pop_input() {
        front.drop_packet(packet);
    }
}

yanet_dp::define_module! {
    loader: new_module_example_rs,
    name: "example_rs",
    handler: handle,
}
