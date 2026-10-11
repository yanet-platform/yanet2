//! Cross-check of the bindgen (libclang) layout against gcc compiling the
//! same headers with the meson flags of a module target.

use yanet_sys::layout_probe::{GCC_LAYOUT, RUST_LAYOUT};

/// Verifies that every probed aggregate and field has the same size,
/// alignment and offset under gcc and in the Rust bindings.
#[test]
fn test_gcc_layout_matches_bindings() {
    assert_eq!(GCC_LAYOUT.len(), RUST_LAYOUT.len());
    assert!(GCC_LAYOUT.len() > 100, "the probe covers too few fields");
    for (gcc, rust) in GCC_LAYOUT.iter().zip(RUST_LAYOUT) {
        assert_eq!(gcc, rust, "gcc and bindgen disagree on {}.{}", gcc.0, gcc.1);
    }
}

/// Verifies that the probe covers every aggregate the SDK declares in its
/// views.
#[test]
fn test_gcc_layout_covers_declared_aggregates() {
    for decl in yanet_sys::views::__LAYOUT_DECL {
        assert!(
            GCC_LAYOUT
                .iter()
                .any(|(aggregate, field, ..)| *aggregate == decl.aggregate && *field == decl.field),
            "{}.{} is declared but not probed with gcc",
            decl.aggregate,
            decl.field
        );
    }
}
