//! Mirror of `lib/dataplane/`: the dataplane boundary of the Rust module SDK.
//!
//! Holds the module trait, the per-invocation context, the prepare context
//! with the handles it makes, the packet batch with owned, branded packets,
//! and the handler runtime the generated plugin exports. Every dataplane
//! `unsafe` of the SDK lives here, in the shared-memory crate and in the
//! common and counters modules of this crate. `no_std` without `alloc`:
//! dataplane memory is shared memory built by the control plane, the prepared
//! buffer or the stack.
//!
//! # Attach assumptions
//!
//! The runtime turns the pointers the dataplane passes to a module into
//! references without runtime checks, trusting the C core as C modules do.
//! SAFETY comments cite these assumptions by name, as `(attach: <name>)`.
//!
//! - **mapping**: the shared memory is one contiguous mapping whose provenance
//!   the execution-context pointer carries.
//! - **call**: the dataplane calls a module's handler with non-null worker,
//!   execution context and packet front of this call, the worker structure live
//!   for the call; the context's configuration is set, its absolute fields are
//!   derived, and its prepared buffer is allocated when the module declares a
//!   prepared size. It calls the commit hook only for an allocated buffer,
//!   after deriving the context's absolute fields, as the single writer of that
//!   buffer.
//! - **binding**: the loader registers the descriptor under the module's name
//!   and the dataplane calls a module's handlers only with contexts of that
//!   module, whose configuration the control plane built with the body type the
//!   module declares.
//! - **frozen**: configuration bytes are frozen after publish and
//!   execution-context bytes once the context is released to its worker; C
//!   writes only `Opaque` bytes and atomics of configurations.
//! - **publish**: C publishes a generation with a release store, workers load
//!   it with acquire, and the commit pass (with the prepare step) completes
//!   before the worker's first handler call of that generation. Besides a
//!   context without a prepared buffer, C skips the commit hook, logging an
//!   error, for a context whose generation has no dataplane configuration or
//!   module slot, which the control plane's validation of the configuration
//!   excludes.
//! - **reclaim**: a generation's blocks are freed only after every worker has
//!   left it.
//! - **packet**: a packet a module sees, with its mbuf, is exclusively owned by
//!   the worker for the call, with no shared or cloned mbuf data reachable. Its
//!   header offsets are the parser's (`parse_packet`, which drops what it
//!   cannot parse): a parsed network header starts after at least the Ethernet
//!   header and no later than the end of the frame, and an IPv4 or IPv6 one
//!   lies whole in the frame; a parsed transport header starts at or after the
//!   network header and no later than the end of the frame, and an available
//!   TCP or UDP one lies whole in it; unparsed headers are zero. The frame is
//!   the first segment: the parser checks against the packet length, and
//!   received packets hold their headers in the first segment. A copy may chain
//!   segments, as in C; reads are bounds-checked to the first segment. Every
//!   packet operation of the SDK keeps this.
//! - **unwind**: no foreign exception unwinds through Rust frames.
//! - **align**: the block allocator aligns a block to at least the smaller of
//!   `MAX_ALIGN` and its size rounded up to a power of two, so the prepared
//!   buffer is aligned for every type the export checks admit.
//!
//! # Module obligations
//!
//! What a module owes the pipeline. Module documentation cites them by name,
//! as `(module: <name>)`.
//!
//! - **drain**: when a handler returns, its input is empty: every input packet
//!   has been passed, dropped or routed to a device through the batch.

pub mod batch;
pub mod cx;
pub mod fields;
pub mod module;
#[doc(hidden)]
pub mod rt;

pub use batch::{Batch, DeviceRef, DscpMark, Packet, PacketView, ResizeError, TunnelError, Verdict};
pub use cx::{BoundCounter, CommitCx, CounterRef, Cx};
pub use fields::{Counter, Device};
pub use module::{Module, ModuleConfig};

/// Items the export macro names; not part of the API.
#[doc(hidden)]
pub mod __private {
    pub use crate::bindings;

    /// Fails const evaluation when the prepared value of a module cannot live
    /// in the dataplane's buffer: the dataplane never drops it, and the block
    /// allocator aligns it to at most `MAX_ALIGN`.
    pub const fn check_prepared<M: crate::dp::Module>() {
        const {
            assert!(
                !core::mem::needs_drop::<M::Prepared<'static>>(),
                "Module::Prepared needs no drop: the dataplane never drops its buffer"
            );
            assert!(
                core::mem::align_of::<M::Prepared<'static>>() <= yanet_shm::shm::MAX_ALIGN,
                "Module::Prepared is aligned beyond what the block allocator guarantees"
            );
        }
    }

    /// Reports whether a module name fits the C module type field
    /// (`MODULE_TYPE_LEN`): non-empty, without NUL, shorter than the field.
    pub const fn fits_module_type(name: &str) -> bool {
        let bytes = name.as_bytes();
        if bytes.is_empty() || bytes.len() >= bindings::MODULE_TYPE_LEN as usize {
            return false;
        }
        let mut idx = 0;
        while idx < bytes.len() {
            if bytes[idx] == 0 {
                return false;
            }
            idx += 1;
        }
        true
    }

    /// Reports whether a loader symbol suffix names a module: the module name
    /// with every `-` written as `_`, as the dataplane's module list names
    /// `route-mpls` by `new_module_route_mpls`.
    pub const fn names_module(symbol: &str, name: &str) -> bool {
        let (symbol, name) = (symbol.as_bytes(), name.as_bytes());
        if symbol.len() != name.len() {
            return false;
        }
        let mut idx = 0;
        while idx < name.len() {
            let expected = if name[idx] == b'-' { b'_' } else { name[idx] };
            if symbol[idx] != expected {
                return false;
            }
            idx += 1;
        }
        true
    }
}
