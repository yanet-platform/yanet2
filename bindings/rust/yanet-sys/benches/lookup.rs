//! Classifier lookups over trees the native Go control plane builds, with
//! oracle checks outside timing.
//!
//! A Go test lays the trees out in one block with the assign fixture's
//! builder and writes it to the file `YANET_CLASSIFY_LOOKUP_TREES` names; `make
//! bench-classify` runs both steps. The block holds relative pointers only,
//! so a copy at the same page alignment reads as the original. Its head
//! carries the keys of each tree and the first rule each key matches, which
//! the Go test computes by a first-match scan of the rules.

extern crate alloc;

use alloc::alloc::{alloc, handle_alloc_error};
use core::{alloc::Layout, hint::black_box, mem::size_of, ptr::NonNull, time::Duration};
use std::{env, fs};

use criterion::{Criterion, Throughput, criterion_group, criterion_main};
use yanet_sdk_testing::View;
use yanet_shm::{
    rel::{ShmBox, ShmSlice},
    root::ShmRoot,
};
use yanet_sys::classify::{Decode, Join, LineAttr, Net4Attr, Net6Attr, PortAttr, Rule, Rules};

/// The environment variable that names the file of the trees.
const TREES: &str = "YANET_CLASSIFY_LOOKUP_TREES";

/// The alignment of the copy of the block: the Go test maps the block at the
/// start of a page, so a copy at the start of a page keeps every block's
/// alignment, up to a page.
const PAGE: usize = 4096;

/// The keys each tree is looked up at: one burst.
const KEYS: usize = 32;

/// The protocol and the destination port joined.
type Transport = Join<LineAttr, PortAttr>;

/// The tree of the IPv4 rules: the networks joined, against the transport.
type Tree4 = Rules<Join<Net4Attr, Net4Attr>, Transport>;

/// The tree of the IPv6 rules, shaped as the IPv4 one.
type Tree6 = Rules<Join<Net6Attr, Net6Attr>, Transport>;

/// The tree of the rules of either family, by protocol and port only.
type TreeAny = Decode<Transport>;

/// An IPv4 key: the addresses, the protocol and the destination port.
#[repr(C)]
struct Key4 {
    src: [u8; 4],
    dst: [u8; 4],
    proto: u8,
    port: u16,
}

/// An IPv6 key.
#[repr(C)]
struct Key6 {
    src: [u8; 16],
    dst: [u8; 16],
    proto: u8,
    port: u16,
}

/// A key of the tree of either family.
#[repr(C)]
struct Key {
    proto: u8,
    port: u16,
}

/// The head of the block, as the Go test writes it: its own size, the
/// trees, and the keys of each tree with the index of the first rule each
/// matches, -1 for none.
#[repr(C)]
struct Head {
    size: u64,
    v4: ShmBox<Tree4>,
    v6: ShmBox<Tree6>,
    any: ShmBox<TreeAny>,
    /// The actions of the assign configuration the trees are built for, which
    /// the block leaves empty.
    _actions: ShmSlice<u8>,
    keys4: [Key4; KEYS],
    want4: [i32; KEYS],
    keys6: [Key6; KEYS],
    want6: [i32; KEYS],
    transport: [Key; KEYS],
    want_any: [i32; KEYS],
}

/// A page-aligned copy of the block the file holds, leaked: its root and its
/// head.
///
/// # Panics
///
/// When the file cannot be read or holds no head of this layout.
fn load() -> (ShmRoot, &'static Head) {
    let path = env::var_os(TREES)
        .unwrap_or_else(|| panic!("{TREES} names no file of trees; run `make bench-classify` at the repository root"));
    let bytes = fs::read(&path).unwrap_or_else(|err| panic!("{}: {err}", path.display()));
    assert!(
        bytes.len() >= size_of::<Head>(),
        "{}: {} bytes hold no head",
        path.display(),
        bytes.len()
    );
    let layout = Layout::from_size_align(bytes.len(), PAGE).expect("block layout");
    // SAFETY: the layout has a non-zero size, at least that of the head.
    let base = NonNull::new(unsafe { alloc(layout) }).unwrap_or_else(|| handle_alloc_error(layout));
    // SAFETY: the copy fills the fresh allocation, which is never freed.
    unsafe { base.copy_from_nonoverlapping(NonNull::from(bytes.as_slice()).cast(), bytes.len()) };
    // SAFETY: the allocation is aligned for the head, holds its bytes, every
    // pattern of which its fields accept, and is never written again.
    let head = unsafe { base.cast::<Head>().as_ref() };
    assert_eq!(
        size_of::<Head>() as u64,
        head.size,
        "{}: the head is of another layout; rebuild it with `make bench-classify`",
        path.display()
    );
    (ShmRoot::from_mapping(base), head)
}

/// The indices of the first rules the Go test's first-match scan found,
/// `None` for none.
fn indices(want: &[i32]) -> Vec<Option<usize>> {
    want.iter().map(|&rule| usize::try_from(rule).ok()).collect()
}

/// Measures the same key batch in scalar and burst form after checking both.
fn measure<K: Copy>(
    criterion: &mut Criterion,
    name: &str,
    keys: &[K],
    expected: &[Option<usize>],
    lookup: impl Fn(K) -> Rule,
    burst: impl Fn(&[K], &mut [Rule]),
) {
    let single = keys.iter().map(|&key| lookup(key).index()).collect::<Vec<_>>();
    assert_eq!(expected, single);
    let mut output = vec![Rule::NONE; keys.len()];
    burst(keys, &mut output);
    let actual = output.iter().map(|rule| rule.index()).collect::<Vec<_>>();
    assert_eq!(expected, actual);

    let mut group = criterion.benchmark_group(name);
    group.throughput(Throughput::Elements(keys.len() as u64));
    group.bench_function("scalar", |bench| {
        bench.iter(|| {
            for &key in black_box(keys) {
                black_box(lookup(black_box(key)));
            }
        });
    });
    group.bench_function("burst", |bench| {
        bench.iter(|| {
            burst(black_box(keys), black_box(&mut output));
            black_box(&output);
        });
    });
    group.finish();
}

fn lookup(criterion: &mut Criterion) {
    let (root, head) = load();
    // SAFETY: the root covers the whole leaked copy of the block, whose
    // trees the Go builder completed and nothing writes again.
    let view = unsafe { View::new(root) };
    let (tree4, tree6, decode) = (head.v4.get(&view), head.v6.get(&view), head.any.get(&view));
    let keys4 = head
        .keys4
        .iter()
        .map(|key| ((key.src, key.dst), (key.proto, key.port)))
        .collect::<Vec<_>>();
    let keys6 = head
        .keys6
        .iter()
        .map(|key| ((key.src, key.dst), (key.proto, key.port)))
        .collect::<Vec<_>>();
    let transport = head
        .transport
        .iter()
        .map(|key| (key.proto, key.port))
        .collect::<Vec<_>>();

    measure(
        criterion,
        "rules4",
        &keys4,
        &indices(&head.want4),
        |key| tree4.lookup(&view, key),
        |keys, output| tree4.lookup_burst(&view, keys, |&key| key, output),
    );
    measure(
        criterion,
        "rules6",
        &keys6,
        &indices(&head.want6),
        |key| tree6.lookup(&view, key),
        |keys, output| tree6.lookup_burst(&view, keys, |&key| key, output),
    );
    measure(
        criterion,
        "decode",
        &transport,
        &indices(&head.want_any),
        |key| decode.lookup(&view, key),
        |keys, output| decode.lookup_burst(&view, keys, |&key| key, output),
    );
}

criterion_group! {
    name = benches;
    config = Criterion::default().sample_size(100).warm_up_time(Duration::from_millis(500)).measurement_time(Duration::from_millis(1500));
    targets = lookup
}
criterion_main!(benches);
