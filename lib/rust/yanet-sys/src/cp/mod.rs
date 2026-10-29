//! Direct configuration writing with the C control plane's ownership rules.
//!
//! The unsafe owner supplies shared memory, headers and registries. Trusted
//! encoders recursively write input fields at their final destinations, with
//! guards for partial values and allocation rollback. The safe builder exposes
//! only typed inputs; source callbacks receive no construction state. Release
//! walks complete owning edges through payload-only blocks.

mod addr;
mod error;
mod init;
mod owner;
mod record;
mod write;

pub use addr::{BlockAddr, Context, Header};
pub use error::{BuildError, Refusal};
pub use init::{
    Boxed, CounterName, DeviceIndex, DeviceMap, DeviceName, LpmInit, Slice, WithDevices, boxed, counter, device, lpm,
    slice, with_devices,
};
pub use owner::Owner;
pub use record::FfiRecord;
pub use write::{Built, Stored, build_config};
#[doc(hidden)]
pub use write::{Encode, FieldGuard, MemoryContext, Writer};
