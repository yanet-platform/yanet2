//! LPM lookup port: Miri proofs over C-built fixtures and the native
//! differential test against the C lookup.
//!
//! Tests named `test_ub_*` are expected to fail under Miri with Undefined
//! Behavior; they are ignored by default and run one by one by
//! `scripts/miri.sh`, which asserts the failure.

use yanet_sdk::{
    Lpm, Shm, ShmLayout,
    lpm::{LPM_VALUE_INVALID, lookup},
};
use yanet_sys::{
    bindings,
    rel::Resolver,
    shm::Validator,
    testing::{CLpm, Fixture, TestArena},
};

/// Offset of the relative page directory in the LPM header.
const LPM_PAGES: usize = core::mem::offset_of!(bindings::lpm, pages);

const LPM4: &[u8] = yanet_sys::testing::LPM4_FIXTURE;
const LPM6: &[u8] = yanet_sys::testing::LPM6_FIXTURE;

/// Small deterministic generator: xorshift64* over a fixed seed.
struct Rng(u64);

impl Rng {
    fn next(&mut self) -> u64 {
        self.0 ^= self.0 >> 12;
        self.0 ^= self.0 << 25;
        self.0 ^= self.0 >> 27;
        self.0.wrapping_mul(0x2545_f491_4f6c_dd1d)
    }

    fn bytes(&mut self) -> [u8; 16] {
        let mut key = [0; 16];
        key[..8].copy_from_slice(&self.next().to_be_bytes());
        key[8..].copy_from_slice(&self.next().to_be_bytes());
        key
    }
}

/// Inclusive range covering a prefix of `len` bits around `addr`.
fn prefix_range(addr: &[u8], len: usize) -> (Vec<u8>, Vec<u8>) {
    let mut from = addr.to_vec();
    let mut to = addr.to_vec();
    for bit in len..addr.len() * 8 {
        let (byte, mask) = (bit / 8, 0x80u8 >> (bit % 8));
        from[byte] &= !mask;
        to[byte] |= mask;
    }
    (from, to)
}

/// Fixed prefix sets of the fixtures: (address, length, value).
///
/// The build-time C generator (`shim/fixture_gen.c` in yanet-sys) must use
/// the same sets; the fixture comparison test fails otherwise.
fn fixture_prefixes(key_size: usize) -> Vec<(Vec<u8>, usize, u32)> {
    if key_size == 4 {
        vec![
            (vec![10, 0, 0, 0], 8, 1),
            (vec![10, 1, 0, 0], 16, 2),
            (vec![10, 1, 2, 0], 24, 3),
            (vec![10, 1, 2, 3], 32, 4),
            (vec![192, 168, 0, 0], 16, 5),
            (vec![172, 16, 0, 0], 12, 6),
        ]
    } else {
        let mut a = vec![0x20, 0x01, 0x0d, 0xb8];
        a.resize(16, 0);
        let mut b = a.clone();
        b[4] = 0x12;
        b[5] = 0x34;
        let mut c = b.clone();
        c[15] = 0x01;
        let mut d = vec![0xfd; 1];
        d.resize(16, 0);
        vec![(a, 32, 11), (b, 48, 12), (c, 128, 13), (d, 8, 14)]
    }
}

/// Keys around every prefix boundary plus a few random ones.
///
/// `shim/fixture_gen.c` in yanet-sys generates the same keys in C.
fn fixture_keys(key_size: usize) -> Vec<[u8; 16]> {
    let mut keys = Vec::new();
    for (addr, len, _) in fixture_prefixes(key_size) {
        let (from, to) = prefix_range(&addr, len);
        for edge in [from, to] {
            let mut key = [0; 16];
            key[..key_size].copy_from_slice(&edge);
            keys.push(key);
            // One below the lower and one above the upper edge.
            for delta in [-1i8, 1] {
                let mut near = key;
                let mut idx = key_size;
                while idx > 0 {
                    idx -= 1;
                    let (value, wrapped) = near[idx].overflowing_add_signed(delta);
                    near[idx] = value;
                    if !wrapped {
                        break;
                    }
                }
                keys.push(near);
            }
        }
    }
    let mut rng = Rng(0x5eed_0000 + key_size as u64);
    keys.extend((0..16).map(|_| rng.bytes()));
    keys
}

/// Builds the fixture LPM with the C insert and captures it.
fn build_fixture(key_size: usize) -> Fixture {
    let arena = TestArena::new(4 << 20);
    let mut lpm = arena.new_lpm();
    for (addr, len, value) in fixture_prefixes(key_size) {
        let (from, to) = prefix_range(&addr, len);
        lpm.insert(&from, &to, value);
    }
    Fixture::capture(&arena, &lpm, key_size as u8, &fixture_keys(key_size))
}

/// Verifies that the build-time C fixtures equal what the in-process C
/// builder and the Rust capture produce, so the Miri replay tests replay
/// exactly the C LPMs.
#[test]
#[cfg_attr(miri, ignore = "runs the C LPM builder")]
fn test_lpm_fixtures_match_c_builder() {
    for (key_size, name, generated) in [(4, "lpm4.bin", LPM4), (16, "lpm6.bin", LPM6)] {
        let encoded = build_fixture(key_size).encode();
        assert!(
            generated == encoded.as_slice(),
            "{name} differs from the in-process C build"
        );
    }
}

fn check_cases<'g, const K: usize, R: Resolver<'g>>(fixture: &Fixture, lpm: Shm<'g, Lpm<K>, R>) {
    assert_eq!(K, usize::from(fixture.key_size));
    for (key, expected) in &fixture.cases {
        let key: &[u8; K] = key[..K].try_into().unwrap();
        assert_eq!(*expected, lookup(lpm, key), "key {key:02x?}");
    }
}

/// Key of `K` bytes from the front of a 16-byte buffer.
fn key_of<const K: usize>(bytes: &[u8; 16]) -> &[u8; K] {
    bytes[..K].try_into().unwrap()
}

#[test]
fn test_lookup_mapping_resolver_matches_c_fixture_v4() {
    let fixture = Fixture::decode(LPM4);
    let (image, root) = fixture.mapping();
    // SAFETY: the fixture is a C-built LPM.
    check_cases(&fixture, unsafe { image.lpm::<4>(root) });
}

#[test]
fn test_lookup_mapping_resolver_matches_c_fixture_v6() {
    let fixture = Fixture::decode(LPM6);
    let (image, root) = fixture.mapping();
    // SAFETY: the fixture is a C-built LPM.
    check_cases(&fixture, unsafe { image.lpm::<16>(root) });
}

/// Verifies that an image copied to a new address, with the original freed,
/// resolves to the same answers: offsets are relative, provenance comes from
/// the new root.
#[test]
fn test_lookup_after_remap_and_free_of_original() {
    remap_case::<4>(LPM4);
    remap_case::<16>(LPM6);
}

fn remap_case<const K: usize>(bytes: &[u8]) {
    let fixture = Fixture::decode(bytes);
    let (image, root) = fixture.mapping();
    // SAFETY: the fixture is a C-built LPM.
    check_cases(&fixture, unsafe { image.lpm::<K>(root) });
    let moved = image.relocate();
    drop(image);
    // SAFETY: a byte copy of a C-built LPM.
    check_cases(&fixture, unsafe { moved.lpm::<K>(root) });
}

/// Verifies that resolution through per-block allocations finds every
/// target inside its own block: no pointer crosses a block boundary.
#[test]
fn test_lookup_block_resolver_matches_c_fixture() {
    block_case::<4>(LPM4);
    block_case::<16>(LPM6);
}

fn block_case<const K: usize>(bytes: &[u8]) {
    let fixture = Fixture::decode(bytes);
    let image = fixture.block_image();
    // SAFETY: the fixture is a C-built LPM.
    check_cases(&fixture, unsafe { image.lpm::<K>(fixture.root) });
}

/// Verifies that a reference to the whole LPM, held across a call during
/// which C rewrites the embedded memory context (the sibling-link bridge of
/// another context's teardown), stays valid: those bytes are opaque.
#[test]
fn test_lookup_survives_c_write_with_opaque_reference() {
    let fixture = Fixture::decode(LPM4);
    let (image, root) = fixture.mapping();
    // SAFETY: the fixture is a C-built LPM.
    let lpm = unsafe { image.lpm::<4>(root) };
    check_cases(&fixture, lpm);
    let sibling = core::mem::offset_of!(bindings::memory_context, next_sibling);
    let count = page_count_across_opaque(lpm.get(), || {
        // SAFETY: in bounds of the image; models the C bridge write through
        // the mapping, not through any Rust reference.
        unsafe { image.ptr_at(root + sibling).cast::<isize>().write(0x40) };
    });
    assert_ne!(0, count);
    check_cases(&fixture, lpm);
}

/// Reads the page count through a whole-LPM reference while `meanwhile`
/// runs; the reference is a protected function argument for the call.
fn page_count_across_opaque(lpm: &Lpm<4>, meanwhile: impl FnOnce()) -> usize {
    meanwhile();
    lpm.page_count()
}

/// Reads the page count through a whole-header reference while `meanwhile`
/// runs, as a handler holding such a reference would.
fn page_count_across(header: &bindings::lpm, meanwhile: impl FnOnce()) -> usize {
    meanwhile();
    header.page_count
}

/// Expected UB: a reference covering the whole C LPM header, held across a
/// call, is violated by the C write to its embedded memory context.
#[test]
#[ignore = "expected UB under Miri; run by scripts/miri.sh"]
fn test_ub_whole_header_reference_across_c_write() {
    let fixture = Fixture::decode(LPM4);
    let (image, root) = fixture.mapping();
    // SAFETY: deliberately wrong: a shared reference over C-mutable bytes.
    let header: &bindings::lpm = unsafe { &*image.ptr_at(root).cast::<bindings::lpm>() };
    let sibling = core::mem::offset_of!(bindings::memory_context, next_sibling);
    let count = page_count_across(header, || {
        // SAFETY: in bounds; the C bridge write.
        unsafe { image.ptr_at(root + sibling).cast::<isize>().write(0x40) };
    });
    assert_ne!(0, count);
}

/// Expected UB under Stacked Borrows only: resolution that takes its
/// provenance from the slot reference instead of the resolver root, the
/// model of a `Copy` offset pointer resolved from `&self`.
#[test]
#[ignore = "expected UB under Stacked Borrows; run by scripts/miri.sh"]
fn test_ub_stacked_slot_reference_provenance() {
    let fixture = Fixture::decode(LPM4);
    let (image, root) = fixture.mapping();
    // SAFETY: the fixture is a C-built LPM.
    let lpm = unsafe { image.lpm::<4>(root) };
    // SAFETY: a reference over the header's directory slot only.
    let slot: &isize = unsafe {
        &*core::ptr::from_ref(lpm.get())
            .cast::<u8>()
            .add(LPM_PAGES)
            .cast::<isize>()
    };
    let directory = core::ptr::from_ref(slot).cast::<u8>().wrapping_offset(*slot);
    // SAFETY: deliberately wrong: the pointer only carries the 8-byte slot
    // reference's permissions, yet reads the chunk directory elsewhere.
    let first_chunk = unsafe { directory.cast::<isize>().read() };
    assert_ne!(0, first_chunk);
}

/// Expected UB: resolving into a freed allocator block.
#[test]
#[ignore = "expected UB under Miri; run by scripts/miri.sh"]
fn test_ub_block_resolver_freed_chunk() {
    let fixture = Fixture::decode(LPM4);
    let image = fixture.block_image();
    let chunk = fixture.blocks[2].0;
    // SAFETY: deliberately frees a block the graph still points at.
    unsafe { image.free_block(chunk) };
    // SAFETY: deliberately broken graph; the test expects the failure.
    check_cases(&fixture, unsafe { image.lpm::<4>(fixture.root) });
}

/// Expected UB: a chunk pointer redirected to the tail of another block, so
/// the chunk would extend past that block's end.
#[test]
#[ignore = "expected UB under Miri; run by scripts/miri.sh"]
fn test_ub_block_resolver_cross_block_overrun() {
    let fixture = Fixture::decode(LPM4);
    let mut image = fixture.block_image();
    let (directory, _) = &fixture.blocks[1];
    let (header, header_bytes) = &fixture.blocks[0];
    // Point the first chunk slot at the last 8 bytes of the header block.
    let target = header + header_bytes.len() - 8;
    image.poke(*directory, target as isize - *directory as isize);
    // SAFETY: deliberately broken graph; the test expects the failure.
    check_cases(&fixture, unsafe { image.lpm::<4>(fixture.root) });
}

/// Verifies that a target outside every block is refused by the block
/// resolver before any access.
#[test]
#[should_panic(expected = "outside every block")]
fn test_block_resolver_refuses_target_outside_blocks() {
    let fixture = Fixture::decode(LPM4);
    let mut image = fixture.block_image();
    let (directory, _) = &fixture.blocks[1];
    image.poke(*directory, -0x10_0000);
    // SAFETY: deliberately broken graph; the test expects the failure.
    check_cases(&fixture, unsafe { image.lpm::<4>(fixture.root) });
}

/// Validates the LPM at `offset` of an image.
fn validate<const K: usize>(image: &yanet_sys::testing::MappedImage, offset: usize) -> Result<(), String> {
    let validator = Validator::new(image);
    <Lpm<K> as ShmLayout>::validate(&validator, image.addr_of(offset)).map_err(|err| err.reason)
}

/// Verifies that both C-built fixtures pass validation, before and after
/// relocation.
#[test]
fn test_validate_accepts_c_built_fixtures() {
    let fixture = Fixture::decode(LPM4);
    let (image, root) = fixture.mapping();
    assert_eq!(Ok(()), validate::<4>(&image, root));
    assert_eq!(Ok(()), validate::<4>(&image.relocate(), root));
    let fixture = Fixture::decode(LPM6);
    let (image, root) = fixture.mapping();
    assert_eq!(Ok(()), validate::<16>(&image, root));
}

/// Offset of the first slot of the root page whose raw value satisfies
/// `pick`, found through the validated fixture.
fn find_slot(image: &yanet_sys::testing::MappedImage, root: usize, pick: impl Fn(u64) -> bool) -> usize {
    use yanet_sys::shm::ShmRead;
    let base = image.addr_of(0);
    let slot = image.addr_of(root + LPM_PAGES);
    let directory = slot.wrapping_add_signed(image.read_u64(slot).unwrap() as isize);
    let chunk = directory.wrapping_add_signed(image.read_u64(directory).unwrap() as isize);
    (0..256)
        .map(|idx| chunk + idx * 8)
        .find(|addr| pick(image.read_u64(*addr).unwrap()))
        .expect("no matching slot")
        - base
}

/// Verifies that corrupt LPM graphs are rejected by validation without any
/// out-of-image access, each for the invariant it breaks.
#[test]
fn test_validate_rejects_corrupt_graphs() {
    let fixture = Fixture::decode(LPM4);
    let page_count = core::mem::offset_of!(bindings::lpm, page_count);
    let (probe, root) = fixture.mapping();
    let child = find_slot(&probe, root, |raw| raw & 1 == 0);
    let leaf = find_slot(&probe, root, |raw| raw & 1 == 1);
    let child_raw = {
        use yanet_sys::shm::ShmRead;
        probe.read_u64(probe.addr_of(child)).unwrap()
    };
    let cases: [(&str, usize, u64, &str); 5] = [
        ("zero page count", root + page_count, 0, "page count is out of range"),
        (
            "huge page count",
            root + page_count,
            1 << 40,
            "page count is out of range",
        ),
        (
            "directory outside the image",
            root + LPM_PAGES,
            1 << 40,
            "outside the owner's memory",
        ),
        ("child not a page start", child, child_raw + 8, "not a page of this LPM"),
        (
            "leaf value out of range",
            leaf,
            (1 << 40) | 1,
            "leaf value is out of range",
        ),
    ];
    for (name, offset, value, expected) in cases {
        let (mut image, root) = fixture.mapping();
        image.write(offset, &value.to_le_bytes());
        let err = validate::<4>(&image, root).expect_err(name);
        assert!(err.contains(expected), "{name}: {err}");
    }
}

/// Rust lookup of a key of the C LPM's key size.
fn lookup_any(lpm: &CLpm<'_>, key_size: usize, bytes: &[u8; 16]) -> u32 {
    match key_size {
        4 => lookup(lpm.view::<4>(), key_of::<4>(bytes)),
        _ => lookup(lpm.view::<16>(), key_of::<16>(bytes)),
    }
}

/// Random prefixes of the given key size: (from, to, value).
fn random_ranges(rng: &mut Rng, key_size: usize, count: usize) -> Vec<(Vec<u8>, Vec<u8>, u32)> {
    (0..count)
        .map(|idx| {
            let addr = rng.bytes();
            let len = match key_size {
                4 => 8 + (rng.next() % 25) as usize,
                _ => 16 + (rng.next() % 113) as usize,
            };
            let (from, to) = prefix_range(&addr[..key_size], len);
            (from, to, idx as u32 + 1)
        })
        .collect()
}

/// Verifies that the Rust port answers exactly like the C lookup on 200k
/// keys, half inside inserted prefixes, on the C arena and on a relocated
/// copy after the arena is freed.
#[test]
#[cfg_attr(miri, ignore = "runs the C LPM")]
fn test_lookup_differential_against_c() {
    for (key_size, prefixes, arena_size) in [(4usize, 4000usize, 32usize << 20), (16, 1000, 128 << 20)] {
        let mut rng = Rng(0xd1ff_0000 + key_size as u64);
        let arena = TestArena::new(arena_size);
        let mut lpm = arena.new_lpm();
        let ranges = random_ranges(&mut rng, key_size, prefixes);
        for (from, to, value) in &ranges {
            lpm.insert(from, to, *value);
        }
        let keys: Vec<[u8; 16]> = (0..200_000)
            .map(|idx| {
                let mut key = rng.bytes();
                if idx % 2 == 0 {
                    // Inside a random inserted range: keep its prefix bytes.
                    let (from, to, _) = &ranges[(rng.next() as usize) % ranges.len()];
                    for byte in 0..key_size {
                        key[byte] = from[byte] | (key[byte] & (from[byte] ^ to[byte]));
                    }
                }
                key
            })
            .collect();
        let expected: Vec<u32> = keys.iter().map(|k| lpm.lookup(&k[..key_size])).collect();
        let hits = expected.iter().filter(|v| **v != LPM_VALUE_INVALID).count();
        assert!(hits > keys.len() / 3, "too few hits: {hits}");

        for (key, value) in keys.iter().zip(&expected) {
            assert_eq!(*value, lookup_any(&lpm, key_size, key));
        }

        let offset = arena.offset_of(lpm.raw().cast());
        let image = arena.copy_image();
        drop(arena);
        // SAFETY: a byte copy of the C-built LPM.
        for (key, value) in keys.iter().zip(&expected) {
            // SAFETY: a byte copy of the C-built LPM.
            let moved = match key_size {
                4 => lookup(unsafe { image.lpm::<4>(offset) }, key_of::<4>(key)),
                _ => lookup(unsafe { image.lpm::<16>(offset) }, key_of::<16>(key)),
            };
            assert_eq!(*value, moved);
            // SAFETY: the copy holds the C-built LPM at the same offset.
            let c_moved = unsafe { image.c_lpm_lookup(offset, &key[..key_size]) };
            assert_eq!(*value, c_moved);
        }
    }
}

// The derive is a dependency of the library, visible to every test target.
use trybuild as _;
use yanet_sdk_derive as _;
