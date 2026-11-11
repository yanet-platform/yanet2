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
//!   buffer. It derives the absolute fields of the object contexts a module
//!   context links before those of the module context
//!   (`config_gen_ectx_resolve_counters`,
//!   `lib/dataplane/config/gen_resolve.c`).
//! - **binding**: the loader registers the descriptor under the module's name
//!   and the dataplane calls a module's handlers only with contexts of that
//!   module, whose configuration the control plane built with the body type the
//!   module declares. An object type is registered under its type name, and the
//!   plugin exporting it, the api crate building its objects and every module
//!   linking one use the one Rust type of that name, so the object a link
//!   resolves to holds the body and prepared value that type declares.
//! - **counters**: C builds a module context with one runtime counter storage
//!   per tagged registry of its configuration, the storage of each registry at
//!   the registry's index, and an object context with the counter storage it
//!   spawns from the object's registry; it builds no context it cannot give
//!   them (`lib/controlplane/config/econtext.c`).
//! - **frozen**: configuration bytes are frozen after publish and
//!   execution-context bytes once the context is released to its worker; C
//!   writes only `Opaque` bytes and atomics of configurations. Shared mutex
//!   payloads change only through a live lock guard. The SDK also allows
//!   per-worker state: a worker's handler writes its own slot or region,
//!   through the handle in its prepared value, and no reference covers the slot
//!   or region but the shared or exclusive borrows the handler takes through
//!   that handle, never both kinds at once.
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
//!   descriptor is initialised throughout, as the receive path and the
//!   allocator zero it before filling it in and a copy takes its source's. Its
//!   header offsets are the parser's (`parse_packet`, which drops what it
//!   cannot parse): a parsed network header starts after at least the Ethernet
//!   header and no later than the end of the frame, and an IPv4 or IPv6 one
//!   lies whole in the frame; a parsed transport header starts at or after the
//!   network header and no later than the end of the frame, and an available
//!   TCP or UDP one lies whole in it; unparsed headers are zero. Here the frame
//!   is the first segment, which holds the headers: the parser checks against
//!   the packet length, and received packets hold their headers in the first
//!   segment. A copy may chain segments, as in C; reads are bounds-checked to
//!   the segment they read. Every packet operation of the SDK keeps this.
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
pub mod object;
mod rt;

pub use batch::{
    Batch, DeviceRef, DscpMark, Packet, PacketMeta, PacketView, ParseError, ResizeError, TunnelError, Verdict,
};
pub use cx::{
    BoundCounter, BoundTaggedCounters, CommitCx, CounterRef, Cx, ModuleCx, ObjectCommitCx, PrepareCx, TaggedCountersRef,
};
pub use fields::{
    Counter, CounterLayout, Device, Gauge, ObjectCounter, ObjectLink, PerWorker, PerWorkerSlice, TaggedCounter,
    TaggedCounters, Traffic, WorkerSlice, WorkerSlot,
};
pub use module::{Module, ModuleConfig};
pub use object::{Object, ObjectConfig};

/// Items the export macros name; not part of the API.
#[doc(hidden)]
pub mod __private {
    pub use crate::{
        bindings,
        dp::rt::{ABI_VERSION, new_module, new_object},
    };

    /// Fails const evaluation when a module cannot be exported: its name does
    /// not fit the C module type field, or its prepared value cannot live in
    /// the dataplane's buffer, which the dataplane never drops and the block
    /// allocator aligns to at most `MAX_ALIGN`; a zero-sized one must have a
    /// value.
    pub const fn check_module<M: crate::dp::Module>() {
        const {
            assert!(
                fits_module_type(M::NAME),
                "Module::NAME must be non-empty, without NUL, and fit the C module type field"
            );
            assert!(
                !core::mem::needs_drop::<M::Prepared<'static>>(),
                "Module::Prepared needs no drop: the dataplane never drops its buffer"
            );
            assert!(
                align_of::<M::Prepared<'static>>() <= yanet_shm::shm::MAX_ALIGN,
                "Module::Prepared is aligned beyond what the block allocator guarantees"
            );
            // A zero-sized value is never written, so the runtime lends one
            // it never made: the type must have a value. `Option` of a type
            // without one has only `None`, which rustc lays out in no bytes.
            assert!(
                size_of::<Option<core::convert::Infallible>>() == 0,
                "rustc no longer lays out Option of a type without a value in no bytes"
            );
            assert!(
                size_of::<M::Prepared<'static>>() != 0 || size_of::<Option<M::Prepared<'static>>>() != 0,
                "Module::Prepared is a zero-sized type without a value"
            );
        }
    }

    /// Fails const evaluation when an object type cannot be exported: its
    /// name does not fit the C object type fields, or its prepared value
    /// cannot live in the dataplane's buffer, as for a module.
    pub const fn check_object<O: crate::dp::Object>() {
        const {
            assert!(
                fits_object_type(O::TYPE),
                "Object::TYPE must be non-empty, without NUL, and fit the C object type fields"
            );
            assert!(
                !core::mem::needs_drop::<O::Prepared<'static>>(),
                "Object::Prepared needs no drop: the dataplane never drops its buffer"
            );
            assert!(
                align_of::<O::Prepared<'static>>() <= yanet_shm::shm::MAX_ALIGN,
                "Object::Prepared is aligned beyond what the block allocator guarantees"
            );
            // A zero-sized value is never written, so the runtime lends one
            // it never made: the type must have a value. `Option` of a type
            // without one has only `None`, which rustc lays out in no bytes.
            assert!(
                size_of::<Option<core::convert::Infallible>>() == 0,
                "rustc no longer lays out Option of a type without a value in no bytes"
            );
            assert!(
                size_of::<O::Prepared<'static>>() != 0 || size_of::<Option<O::Prepared<'static>>>() != 0,
                "Object::Prepared is a zero-sized type without a value"
            );
        }
    }

    /// Reports whether a module name fits the C module type field
    /// (`MODULE_TYPE_LEN`): non-empty, without NUL, shorter than the field.
    const fn fits_module_type(name: &str) -> bool {
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

    /// Reports whether an object type name fits the C object type fields
    /// (`OBJECT_TYPE_LEN`, `CP_OBJECT_TYPE_LEN`): non-empty, without NUL,
    /// shorter than either field.
    const fn fits_object_type(name: &str) -> bool {
        let bytes = name.as_bytes();
        let len = if bindings::OBJECT_TYPE_LEN < bindings::CP_OBJECT_TYPE_LEN {
            bindings::OBJECT_TYPE_LEN
        } else {
            bindings::CP_OBJECT_TYPE_LEN
        };
        if bytes.is_empty() || bytes.len() >= len as usize {
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

    /// Reports whether a loader symbol suffix names an object type: the type
    /// name as it is, as the dataplane looks a type up by
    /// `new_object_<type>`.
    pub const fn names_object(symbol: &str, name: &str) -> bool {
        let (symbol, name) = (symbol.as_bytes(), name.as_bytes());
        if symbol.len() != name.len() {
            return false;
        }
        let mut idx = 0;
        while idx < name.len() {
            if symbol[idx] != name[idx] {
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
