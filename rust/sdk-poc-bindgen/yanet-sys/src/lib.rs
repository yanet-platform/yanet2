//! Unsafe boundary of the YANET2 Rust module SDK.
//!
//! Everything `unsafe` the SDK needs lives here: the bindgen bindings of the
//! C headers, self-relative pointer resolution with strict provenance, the
//! declared views over C aggregates, the worker-owned packet front, and the
//! module export macro. Module crates build on the safe surface and carry
//! `#![forbid(unsafe_code)]`.

#[allow(
    non_camel_case_types,
    non_upper_case_globals,
    non_snake_case,
    dead_code,
    unnecessary_transmutes,
    clippy::all
)]
pub mod bindings {
    include!(concat!(env!("OUT_DIR"), "/bindings.rs"));
}

#[doc(hidden)]
pub mod bindgen_fields {
    include!(concat!(env!("OUT_DIR"), "/bindgen_fields.rs"));
}

/// Field layouts as gcc and as the Rust bindings see them, for the layout
/// cross-check test.
#[doc(hidden)]
pub mod layout_probe {
    include!(concat!(env!("OUT_DIR"), "/gcc_layout.rs"));
    include!(concat!(env!("OUT_DIR"), "/rust_layout.rs"));
}

#[cfg(any(feature = "cp", feature = "testing"))]
pub mod builder;
#[cfg(feature = "cp")]
pub mod cp;
pub mod layout;
pub mod lpm;
#[cfg(feature = "dp")]
pub mod module;
#[cfg(feature = "dp")]
pub mod packet;
pub mod rel;
pub mod shm;
#[cfg(feature = "testing")]
#[doc(hidden)]
pub mod testing;
pub mod views;
