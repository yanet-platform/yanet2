//! Out-of-line instances of the hot-path primitives, for reading their
//! machine code next to the C ones (`scripts/asm.sh`).

use core::hint::black_box;

use yanet_sdk::{Lpm, Shm, lpm::lookup};
use yanet_sys::{
    rel::{MapResolver, RelRef, Resolver},
    testing::TestArena,
    views::LpmPage,
};

/// Strict-provenance non-null resolution; the C twin is `ADDR_OF_NONNULL`.
#[unsafe(no_mangle)]
#[inline(never)]
pub fn yanet_probe_resolve_ref<'g>(res: MapResolver<'g>, slot: &'g RelRef<LpmPage>) -> &'g LpmPage {
    res.resolve_ref(slot)
}

/// IPv4 LPM lookup; the C twin is `lpm_lookup` with a 4-byte key.
#[unsafe(no_mangle)]
#[inline(never)]
pub fn yanet_probe_lookup4(lpm: Shm<'_, Lpm<4>, MapResolver<'_>>, key: &[u8; 4]) -> u32 {
    lookup(lpm, key)
}

fn main() {
    let arena = TestArena::new(2 << 20);
    let mut lpm = arena.new_lpm();
    lpm.insert(&[10, 0, 0, 0], &[10, 255, 255, 255], 1);
    let view = lpm.view::<4>();
    println!("{}", yanet_probe_lookup4(black_box(view), black_box(&[10, 1, 2, 3])));
    if let Some(page) = view.root_page()
        && let yanet_sys::lpm::LpmEntry::Child(child) = page.values()[10].entry()
    {
        println!("{:p}", yanet_probe_resolve_ref(view.resolver(), black_box(child)));
    }
}

// The derive is a dependency of the library, visible to every example.
use trybuild as _;
use yanet_sdk_derive as _;
