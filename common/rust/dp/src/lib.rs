//! YANET dataplane module SDK: write a module dataplane in Rust with no
//! unsafe code of its own.
//!
//! A module is one [`define_module!`] invocation plus a handler
//! function. The macro exports the `new_module_<name>` loader symbol the
//! dataplane resolves, and every C structure a module touches — the
//! packet front, the execution context, the shared-memory config, the
//! classifiers — reaches module code only through the safe wrappers
//! here. All `unsafe` blocks live inside this crate and
//! `yanet-shm`, each carrying its soundness argument.
//!
//! Layouts are pinned three ways: compile-time asserts against
//! `plugin_abi_assert.h` values ([`raw`]), `#[no_mangle]` size and
//! offset getters cross-checked by the dataplane_ut C test ([`abi`]),
//! and the exported `yanet_module_abi_version`.
//!
//! Panics in a handler abort the dataplane (the workspace pins
//! `panic = "abort"` for release), exactly like a C crash; there is no
//! unwinding across the FFI boundary and no error path out of a handler.
#![no_std]

mod abi;
pub mod raw;

mod ectx;
mod front;
mod module;
mod packet;

pub use ectx::{Counter, DeviceTarget, Ectx, ObjectLink};
pub use front::{PacketFront, PacketListBuilder};
pub use module::{
    ModuleCommit, ModuleCommitEctx, ModuleHandler, commit_ectx_trampoline, commit_trampoline, handler_trampoline,
    module_descriptor,
};
pub use packet::Packet;
pub use yanet_shm;

/// The names a module usually wants in scope.
pub mod prelude {
    pub use crate::ectx::{Counter, DeviceTarget, Ectx, ObjectLink};
    pub use crate::front::PacketFront;
    pub use crate::module::{ModuleCommit, ModuleCommitEctx, ModuleHandler};
    pub use crate::packet::Packet;
    pub use crate::raw::YANET_MODULE_ABI_VERSION;
    pub use crate::{define_module, yanet_shm};
    pub use yanet_packet as packet;
    pub use yanet_shm::{CpModule, LPM_VALUE_INVALID, Lpm, ModuleConfig, ObjectConfig};
}

#[cfg(test)]
extern crate std;

#[cfg(test)]
mod tests;
