//! The Rust decap api builds validated configurations whose LPMs answer
//! exactly like LPMs built by the C decap api's insert.

use core::ptr::NonNull;

use decap_api::{ApiError, Decap, DecapConfig, Prefix, build};
use yanet_sdk::{Shm, lpm::lookup};
use yanet_sys::{builder::ConfigBuilder, rel::MapResolver, shm::ModuleConfig, testing::TestArena};

fn v4(from: [u8; 4], to: [u8; 4]) -> Prefix {
    Prefix::V4 { from, to }
}

/// Prefixes of the test configuration and the same ranges for the C LPM.
fn prefixes() -> Vec<Prefix> {
    let mut from6 = [0u8; 16];
    from6[..4].copy_from_slice(&[0x20, 0x01, 0x0d, 0xb8]);
    let mut to6 = [0xffu8; 16];
    to6[..4].copy_from_slice(&[0x20, 0x01, 0x0d, 0xb8]);
    vec![
        v4([10, 0, 0, 0], [10, 255, 255, 255]),
        v4([192, 0, 2, 7], [192, 0, 2, 7]),
        Prefix::V6 { from: from6, to: to6 },
    ]
}

/// View of a handed-over configuration in a test arena.
///
/// # Safety
///
/// `config` must be a validated decap configuration inside `arena`.
unsafe fn view(
    arena: &TestArena,
    config: NonNull<core::ffi::c_void>,
) -> Shm<'_, ModuleConfig<DecapConfig>, MapResolver<'_>> {
    // SAFETY: guaranteed by the caller; the arena root covers it.
    unsafe {
        let res = MapResolver::new(NonNull::new(arena.base()).unwrap());
        Shm::from_raw(res, res.at(config.addr().get()))
    }
}

/// Verifies that a built configuration answers like a C-built LPM on
/// random keys of both families.
#[test]
fn test_build_answers_like_c_lpm() {
    let arena = TestArena::new(8 << 20);
    let config = build(&arena, "decap0", &prefixes()).expect("build");
    let mut c4 = arena.new_lpm();
    let mut c6 = arena.new_lpm();
    for prefix in prefixes() {
        match prefix {
            Prefix::V4 { from, to } => c4.insert(&from, &to, 1),
            Prefix::V6 { from, to } => c6.insert(&from, &to, 1),
        }
    }
    // SAFETY: validated by build.
    let view = unsafe { view(&arena, config) };
    let body = view.map(ModuleConfig::body);
    let mut state = 0x9e37_79b9_7f4a_7c15u64;
    for _ in 0..50_000 {
        state ^= state << 13;
        state ^= state >> 7;
        state ^= state << 17;
        let bytes = state.to_be_bytes();
        let mut k4: [u8; 4] = bytes[..4].try_into().unwrap();
        if state.is_multiple_of(3) {
            k4[0] = 10;
        }
        let mut k6 = [0u8; 16];
        k6[8..].copy_from_slice(&bytes);
        if state.is_multiple_of(2) {
            k6[..4].copy_from_slice(&[0x20, 0x01, 0x0d, 0xb8]);
        }
        assert_eq!(c4.lookup(&k4), lookup(body.map(|c| &c.prefixes4), &k4), "{k4:?}");
        assert_eq!(c6.lookup(&k6), lookup(body.map(|c| &c.prefixes6), &k6), "{k6:02x?}");
    }
}

/// Verifies that malformed requests are refused before anything is built.
#[test]
fn test_build_rejects_malformed_requests() {
    let arena = TestArena::new(4 << 20);
    assert!(matches!(build(&arena, "", &[]), Err(ApiError::InvalidArgument(_))));
    let reversed = v4([10, 0, 0, 9], [10, 0, 0, 1]);
    assert!(matches!(
        build(&arena, "decap0", &[reversed]),
        Err(ApiError::InvalidArgument(_))
    ));
}

/// Verifies that validation rejects a configuration whose LPM graph was
/// corrupted after it was built.
#[test]
fn test_validate_rejects_corrupt_configuration() {
    let arena = TestArena::new(8 << 20);
    let lpm4 = ModuleConfig::<DecapConfig>::BODY_OFFSET + core::mem::offset_of!(DecapConfig, prefixes4);
    let lpm6 = ModuleConfig::<DecapConfig>::BODY_OFFSET + core::mem::offset_of!(DecapConfig, prefixes6);
    let pages = core::mem::offset_of!(yanet_sys::bindings::lpm, pages);
    let page_count = core::mem::offset_of!(yanet_sys::bindings::lpm, page_count);
    let cases = [
        ("IPv4 page directory", lpm4 + pages, "outside the owner's memory"),
        ("IPv6 page count", lpm6 + page_count, "page count is out of range"),
    ];
    for (name, offset, expected) in cases {
        let mut config: ConfigBuilder<'_, Decap, TestArena> = arena.build("decap0").unwrap();
        config
            .insert(|c| &c.prefixes4, &[10, 0, 0, 0], &[10, 255, 255, 255], 1)
            .unwrap();
        assert_eq!(Ok(()), config.validate(), "{name}: valid before corruption");
        let word = arena.base().with_addr(config.addr() + offset).cast::<u64>();
        // SAFETY: an aligned word inside the configuration block.
        unsafe { word.write(word.read() ^ (1 << 40)) };
        let err = config.validate().expect_err(name);
        assert!(err.0.contains(expected), "{name}: {err}");
        // The builder frees its LPMs on drop, which needs the intact graph.
        // SAFETY: as above.
        unsafe { word.write(word.read() ^ (1 << 40)) };
    }
}

// The library's own dependency, visible to every test target.
use decap_dp as _;
