//! Configuration layout of the decap module.
//!
//! In a full SDK this would live in a per-module sys crate or be generated
//! from a layout declaration; it is here to keep the proof of concept small.

use core::ops::Range;

use crate::{ConfigLayout, LpmError, LpmView, Root, ffi::decap_module_config, lpm::validate_lpm};

/// Read-only view of a published decap configuration.
///
/// The `cp_module` header in front of the prefixes is C-owned and mutated
/// under the control-plane lock after publication; the view never covers it.
#[derive(Clone, Copy)]
pub struct DecapConfig<'g> {
    prefixes4: LpmView<'g>,
    prefixes6: LpmView<'g>,
}

impl<'g> DecapConfig<'g> {
    /// Tunnel endpoints matched on the outer IPv4 destination.
    pub fn prefixes4(&self) -> &LpmView<'g> {
        &self.prefixes4
    }

    /// Tunnel endpoints matched on the outer IPv6 destination.
    pub fn prefixes6(&self) -> &LpmView<'g> {
        &self.prefixes6
    }
}

/// Layout marker of the decap configuration.
pub enum DecapLayout {}

impl crate::sealed::Sealed for DecapLayout {}

impl ConfigLayout for DecapLayout {
    const TYPE_NAME: &'static str = "decap";

    type View<'g> = DecapConfig<'g>;

    #[inline]
    unsafe fn attach<'g>(root: Root<'g>) -> DecapConfig<'g> {
        let config = root.as_ptr().cast::<decap_module_config>();
        // SAFETY: the caller guarantees the root points at a validated decap
        // configuration; the projections stay raw until the LPM views borrow
        // only their own frozen fields.
        unsafe {
            DecapConfig {
                prefixes4: LpmView::attach(root, &raw const (*config).prefixes4),
                prefixes6: LpmView::attach(root, &raw const (*config).prefixes6),
            }
        }
    }
}

/// Validates both prefix trees of the configuration at `root`.
///
/// No page table or page may overlap the configuration structure itself,
/// which holds the C-mutable module header and memory contexts. Other
/// C-mutable bytes elsewhere in `bounds` are not known here.
///
/// # Safety
///
/// Same contract as [`validate_lpm`], with `root` pointing at a
/// `struct decap_module_config` inside `bounds`.
pub unsafe fn validate(root: Root<'_>, bounds: Range<usize>) -> Result<(), LpmError> {
    let config = root.as_ptr().cast::<decap_module_config>();
    let header = config.addr()..config.addr() + size_of::<decap_module_config>();
    let header = core::slice::from_ref(&header);
    // SAFETY: forwarded caller contract; the field projections do not read.
    unsafe {
        validate_lpm(root, &raw const (*config).prefixes4, bounds.clone(), header)?;
        validate_lpm(root, &raw const (*config).prefixes6, bounds, header)
    }
}

#[cfg(test)]
mod tests {
    use core::ptr::NonNull;

    use yanet_testkit::{RawBuf, fixture};

    use super::{DecapLayout, validate};
    use crate::{
        ConfigLayout, Root,
        ffi::{decap_module_config, lpm, memory_context},
    };

    /// Asserts the fixture's recorded C results through `config`.
    fn assert_results(config: &super::DecapConfig<'_>) {
        for (key, expected) in fixture::keys4() {
            assert_eq!(expected, config.prefixes4().lookup(&key));
        }
        for (key, expected) in fixture::keys6() {
            assert_eq!(expected, config.prefixes6().lookup(&key));
        }
    }

    /// Writes C performs on a published configuration: the registry
    /// reference count under the control-plane lock and the memory-context
    /// sibling link when a neighbouring context is finalised.
    ///
    /// # Safety
    ///
    /// `config` must point at a decap configuration inside a live allocation.
    unsafe fn c_header_writes(config: NonNull<u8>) {
        let config = config.as_ptr().cast::<decap_module_config>();
        // SAFETY: raw place projections into the live configuration; no
        // reference is formed.
        unsafe {
            let header = &raw mut (*config).cp_module;
            (&raw mut (*header).config_item.refcnt).write(3);
            let context: *mut memory_context = &raw mut (*config).prefixes4.memory_context;
            (&raw mut (*context).next_sibling).write(header.cast());
            (&raw mut (*context).balloc_count).write(0xdead);
        }
    }

    /// Runs the C header writes while `config` is live as a protected
    /// argument, the way a module handler holds it while C mutates the
    /// header from another thread, then reads through it again.
    fn write_header_while_holding_view(config: super::DecapConfig<'_>, header: NonNull<u8>) {
        // SAFETY: the configuration lies inside the live copy.
        unsafe { c_header_writes(header) };
        assert_results(&config);
    }

    /// Verifies that the attached views stay valid across the writes C makes
    /// to the configuration header after publication, because no Rust
    /// reference covers those bytes; Miri checks this under both aliasing
    /// models.
    #[test]
    fn test_attach_survives_c_header_writes() {
        let buf = RawBuf::from_bytes(fixture::IMAGE);
        let config_ptr = buf.ptr_at(fixture::CONFIG_OFFSET);
        // SAFETY: the copy is a C-built configuration; validation precedes
        // attaching, and only the C-owned header bytes are written below.
        let config = unsafe {
            let root = Root::from_raw(config_ptr);
            validate(root, buf.bounds()).expect("C-built configuration is valid");
            DecapLayout::attach(root)
        };
        assert_results(&config);
        write_header_while_holding_view(config, config_ptr);
    }

    /// Reads the page count through a whole-struct reference while the C
    /// header writes run.
    fn write_header_while_holding_whole(whole: &lpm, header: NonNull<u8>) -> usize {
        // SAFETY: deliberately unsound: the protected reference covers the
        // embedded memory context that this write modifies.
        unsafe { c_header_writes(header) };
        whole.page_count
    }

    /// Asserts that a shared reference over a whole tree, as a naive binding
    /// would form, conflicts with the same C write while it is held.
    ///
    /// A negative control: Miri must report undefined behaviour here.
    #[test]
    #[ignore = "negative control: Miri must report undefined behaviour"]
    fn test_whole_struct_reference_conflicts_with_c_header_write() {
        let buf = RawBuf::from_bytes(fixture::IMAGE);
        let config_ptr = buf.ptr_at(fixture::CONFIG_OFFSET);
        let config = config_ptr.as_ptr().cast::<decap_module_config>();
        // SAFETY: the tree lies inside the live copy; the reference is the
        // unsound part under test.
        let whole: &lpm = unsafe { &(*config).prefixes4 };
        assert_ne!(0, write_header_while_holding_whole(whole, config_ptr));
    }
}
