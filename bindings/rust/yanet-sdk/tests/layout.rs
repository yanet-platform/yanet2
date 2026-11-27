//! The layout description the Go views are generated from: the derive's is
//! Rust's own sizes, alignments and offsets, private fields included, and each
//! SDK field type lists every byte of itself.

use core::{assert_matches, sync::atomic::AtomicU64};

use yanet_sdk::{
    Counter, Decode, Join, LineAttr, Net4Attr, Object, ObjectCommitCx, ObjectCounter, ObjectLink, PerWorker,
    PerWorkerSlice, PortAttr, Rules, ShmBox, ShmLayout, ShmSlice, Traffic,
    layout::{Kind, Node},
};

/// A struct of private fields, the first followed by padding.
#[derive(ShmLayout)]
#[repr(C)]
struct Inner {
    tag: u16,
    value: u64,
}

/// A struct of integers, a struct, an atomic and SDK field types, aligned past
/// what its fields need.
#[derive(ShmLayout)]
#[repr(C, align(64))]
struct Outer {
    small: u8,
    inner: Inner,
    atomic: AtomicU64,
    boxed: ShmBox<Inner>,
    slice: ShmSlice<[u16; 3]>,
    counter: Counter<Traffic>,
}

/// An object type, for a link to it.
struct State;

impl Object for State {
    const TYPE: &'static str = "layout_state";
    type Config = Inner;
    type Prepared<'p> = ();

    fn prepare(_: &ObjectCommitCx<'_, Self>) {}
}

/// The name, the offset and the size of each field the description gives.
fn fields(node: &Node) -> Vec<(&'static str, usize, usize)> {
    let Kind::Struct { fields, .. } = &node.kind else {
        panic!("not a struct");
    };
    fields
        .iter()
        .map(|field| (field.name, field.offset, field.ty.size))
        .collect()
}

/// The byte ranges the members of an SDK field type take, in order.
fn member_ranges(node: &Node) -> Vec<(usize, usize)> {
    let Kind::Edge { members, .. } = &node.kind else {
        panic!("not an SDK field type");
    };
    let mut ranges: Vec<_> = members
        .iter()
        .map(|member| (member.offset, member.offset + member.ty.size))
        .collect();
    ranges.sort_unstable();
    ranges
}

/// Verifies that the derive describes the size, the alignment and every
/// field of a struct as the C layout rules place them.
#[test]
fn test_layout_derive_describes_fields() {
    assert_eq!((16, 8), (Inner::LAYOUT.size, Inner::LAYOUT.align));
    assert_eq!(vec![("tag", 0, 2), ("value", 8, 8)], fields(Inner::LAYOUT));
    assert_eq!((64, 64), (Outer::LAYOUT.size, Outer::LAYOUT.align));
    assert_eq!(
        vec![
            ("small", 0, 1),
            ("inner", 8, 16),
            ("atomic", 24, 8),
            ("boxed", 32, 8),
            ("slice", 40, 16),
            ("counter", 56, 8),
        ],
        fields(Outer::LAYOUT),
    );
}

/// Verifies that the members of each SDK field type cover its bytes, each
/// right after the previous one, so a word added without a member fails here.
#[test]
fn test_layout_edge_members_cover_their_bytes() {
    type Pair = Join<Net4Attr, Net4Attr>;
    for node in [
        <ShmBox<u64>>::LAYOUT,
        <ShmSlice<u64>>::LAYOUT,
        <Counter<Traffic>>::LAYOUT,
        <ObjectCounter>::LAYOUT,
        <ObjectLink<State>>::LAYOUT,
        <PerWorker<u64>>::LAYOUT,
        <PerWorkerSlice<u64>>::LAYOUT,
        Pair::LAYOUT,
        <Rules<Pair, Join<LineAttr, PortAttr>>>::LAYOUT,
        <Decode<Pair>>::LAYOUT,
    ] {
        let mut end = 0;
        for (start, next) in member_ranges(node) {
            assert_eq!(end, start);
            end = next;
        }
        assert_eq!(node.size, end);
    }
}

#[test]
fn test_layout_integers_describe_their_signedness() {
    assert_matches!(i16::LAYOUT.kind, Kind::Int { signed: true, atomic: false });
    assert_matches!(u16::LAYOUT.kind, Kind::Int { signed: false, atomic: false });
}
