//! Generic module configuration: an opaque C header followed by a body whose
//! mirror the module itself declares.
//!
//! The module's C header stays the source of truth for the body layout; the
//! module crate mirrors the body with zerocopy's `FromBytes` and `KnownLayout`
//! derives, and a per-module systest checks that mirror against the header.

use core::{
    mem::{align_of, offset_of, size_of},
    ops::Range,
    ptr,
};

use zerocopy::{FromBytes, KnownLayout};

use crate::{
    LpmView, Root,
    ffi::{Opaque, cp_module, lpm},
};

/// Layout of a C module configuration: `struct cp_module` at offset 0, then
/// the module's own fields.
///
/// A C configuration `struct { struct cp_module cp_module; T1 f1; T2 f2; }`
/// has the same layout as `ModuleConfig<B>` when `B` is a `repr(C)` struct
/// of `f1` and `f2` and its alignment does not exceed the header's; the
/// per-module systest checks that equivalence.
#[repr(C)]
pub struct ModuleConfig<B> {
    header: Opaque<cp_module>,
    body: B,
}

/// Read-only handle to one published module configuration.
pub struct ConfigView<'g, B> {
    root: Root<'g>,
    body: &'g B,
}

impl<'g, B: FromBytes + KnownLayout> ConfigView<'g, B> {
    /// Attaches to the configuration whose `cp_module` header `root` points at.
    ///
    /// The body reference is formed by a plain pointer cast, not by
    /// zerocopy's checked conversions: those require `Immutable`, which a
    /// body embedding C-mutable bytes cannot be. The cast is sound because
    /// the body type is `FromBytes`, so every initialised byte pattern is a
    /// valid value; `KnownLayout` and `Sized` fix its size and alignment, the
    /// alignment is checked below, and C-mutable bytes inside it are
    /// `Opaque`, so the shared reference never asserts they stay unchanged.
    ///
    /// # Safety
    ///
    /// `root` must satisfy the [`Root::from_raw`] contract and point at a
    /// live configuration of at least `size_of::<ModuleConfig<B>>()`
    /// initialised bytes whose layout is the C layout `B` mirrors, validated
    /// before publication, and published for `'g`. Every byte of the body
    /// that C writes after publication must lie inside an `Opaque` field.
    #[inline]
    pub unsafe fn attach(root: Root<'g>) -> Self {
        let config = root.as_ptr().cast::<ModuleConfig<B>>();
        assert!(config.is_aligned(), "module configuration is misaligned for its mirror");
        // SAFETY: the caller guarantees the bytes are live, initialised and
        // laid out as `B`; `B: FromBytes` accepts any such bytes, and the
        // projection never covers the opaque header.
        let body = unsafe { &*ptr::addr_of!((*config).body) };
        Self { root, body }
    }

    /// The module's own configuration fields.
    pub fn body(&self) -> &'g B {
        self.body
    }

    /// View of an LPM tree embedded in the body.
    ///
    /// # Panics
    ///
    /// Panics if `tree` does not lie inside this configuration's body: a tree
    /// built elsewhere (a `FromBytes` value conjured by the module) carries
    /// no validated graph, so resolving it would read arbitrary memory.
    pub fn lpm(&self, tree: &'g lpm) -> LpmView<'g> {
        let body = body_range::<B>(self.body);
        let start = ptr::from_ref(tree).addr();
        assert!(
            body.start <= start && start + size_of::<lpm>() <= body.end && start.is_multiple_of(align_of::<lpm>()),
            "LPM tree outside the configuration body"
        );
        // SAFETY: the tree is a field of the attached body, so the attach
        // contract covers its validated graph and the root's provenance.
        unsafe { LpmView::attach(self.root, tree) }
    }
}

/// Address range of a body reference.
fn body_range<B>(body: &B) -> Range<usize> {
    let start = ptr::from_ref(body).addr();
    start..start + size_of::<B>()
}

/// Byte range of the whole configuration at `root`, header included.
///
/// The control plane passes it to the LPM validator as C-mutable bytes no
/// page may overlap.
pub fn config_range<B>(root: Root<'_>) -> Range<usize> {
    let start = root.as_ptr().addr();
    start..start + size_of::<ModuleConfig<B>>()
}

/// Offset of the body inside the configuration.
pub const fn body_offset<B>() -> usize {
    offset_of!(ModuleConfig<B>, body)
}

#[cfg(test)]
mod tests {
    use core::ptr::NonNull;

    use yanet_testkit::{RawBuf, fixture};
    use zerocopy::FromZeros;

    use super::{ConfigView, body_offset, config_range};
    use crate::{
        Lpm, Root,
        ffi::{RelPtr, cp_module, lpm_page, memory_context},
        lpm::validate_lpm,
        test_body::TwoTrees,
    };

    /// Attached view of a validated copy of the C-built fixture.
    ///
    /// # Safety
    ///
    /// Only the C-owned header and memory-context bytes of the copy may be
    /// written while the view is used.
    unsafe fn attach(buf: &RawBuf) -> ConfigView<'_, TwoTrees> {
        // SAFETY: the copy is a C-built configuration of this layout; both
        // trees are validated before attaching.
        unsafe {
            let root = Root::from_raw(buf.ptr_at(fixture::CONFIG_OFFSET));
            let mutable = config_range::<TwoTrees>(root);
            let body = root.as_ptr().add(body_offset::<TwoTrees>()).cast::<TwoTrees>();
            for tree in [&raw const (*body).first, &raw const (*body).second] {
                validate_lpm(root, tree, buf.bounds(), core::slice::from_ref(&mutable)).expect("valid C tree");
            }
            ConfigView::attach(root)
        }
    }

    /// Asserts the fixture's recorded C results through both trees.
    fn assert_results(config: &ConfigView<'_, TwoTrees>) {
        let (v4, v6) = (config.lpm(&config.body().first), config.lpm(&config.body().second));
        for (key, expected) in fixture::keys4() {
            assert_eq!(expected, v4.lookup(&key));
        }
        for (key, expected) in fixture::keys6() {
            assert_eq!(expected, v6.lookup(&key));
        }
    }

    /// Writes C performs on a published configuration: the registry
    /// reference count under the control-plane lock and the memory-context
    /// sibling link when a neighbouring context is finalised.
    ///
    /// # Safety
    ///
    /// `config` must point at a two-tree configuration inside a live
    /// allocation.
    unsafe fn c_header_writes(config: NonNull<u8>) {
        let config = config.as_ptr();
        // SAFETY: raw place writes into the live configuration at the C
        // field offsets; no reference is formed.
        unsafe {
            let header = config.cast::<cp_module>();
            (&raw mut (*header).config_item.refcnt).write(3);
            let context = config.add(body_offset::<TwoTrees>()).cast::<memory_context>();
            (&raw mut (*context).next_sibling).write(header.cast());
            (&raw mut (*context).balloc_count).write(0xdead);
        }
    }

    /// Runs the C header writes while the body reference is live as a
    /// protected argument, the way a module holds it while C mutates the
    /// header from another thread, then reads through it again.
    fn write_header_while_holding_body(config: &ConfigView<'_, TwoTrees>, body: &TwoTrees, header: NonNull<u8>) {
        // SAFETY: the configuration lies inside the live copy.
        unsafe { c_header_writes(header) };
        assert_eq!(config.body().first.page_count, body.first.page_count);
        assert_results(config);
    }

    /// Verifies that a shared reference to the whole body, header-adjacent
    /// memory contexts included, stays valid across the writes C makes after
    /// publication, because those bytes are opaque; Miri checks this under
    /// both aliasing models.
    #[test]
    fn test_body_reference_survives_c_header_writes() {
        let buf = RawBuf::from_bytes(fixture::IMAGE);
        // SAFETY: only header and memory-context bytes are written below.
        let config = unsafe { attach(&buf) };
        assert_results(&config);
        write_header_while_holding_body(&config, config.body(), buf.ptr_at(fixture::CONFIG_OFFSET));
    }

    /// A naive tree mirror whose embedded memory context is not opaque.
    #[repr(C)]
    struct NaiveLpm {
        memory_context: memory_context,
        pages: RelPtr<RelPtr<lpm_page>>,
        page_count: usize,
    }

    /// Reads the page count through a naive whole-tree reference while the
    /// C header writes run.
    fn write_header_while_holding_naive(naive: &NaiveLpm, header: NonNull<u8>) -> usize {
        // SAFETY: deliberately unsound: the protected reference freezes the
        // memory context that this write modifies.
        unsafe { c_header_writes(header) };
        naive.page_count
    }

    /// Asserts that a naive mirror, without the opaque memory context,
    /// conflicts with the same C write while a reference to it is held.
    ///
    /// A negative control: Miri must report undefined behaviour here.
    #[test]
    #[ignore = "negative control: Miri must report undefined behaviour"]
    fn test_naive_mirror_conflicts_with_c_header_write() {
        let buf = RawBuf::from_bytes(fixture::IMAGE);
        let config = buf.ptr_at(fixture::CONFIG_OFFSET);
        // SAFETY: the tree lies inside the live copy; the naive layout is the
        // unsound part under test.
        let naive = unsafe { &*config.as_ptr().add(body_offset::<TwoTrees>()).cast::<NaiveLpm>() };
        assert_ne!(0, write_header_while_holding_naive(naive, config));
    }

    /// Verifies that a tree conjured outside the configuration, which
    /// `FromBytes` lets safe code create, is refused instead of resolved.
    #[test]
    #[should_panic(expected = "LPM tree outside the configuration body")]
    fn test_lpm_rejects_tree_outside_body() {
        let conjured = Lpm::new_zeroed();
        let buf = RawBuf::from_bytes(fixture::IMAGE);
        // SAFETY: the copy is not written.
        let config = unsafe { attach(&buf) };
        let _ = config.lpm(&conjured);
    }
}
