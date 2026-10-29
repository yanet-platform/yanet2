//! Native classifier input records, distinct from graph node markers.

pub use crate::bindings::{
    classify_line_range as LineRange, filter_port_range as PortRange, net4 as Net4, net6 as Net6,
};

// SAFETY: native records contain only byte arrays or integer bounds; every bit
// pattern is valid and bindgen asserts their native layout.
unsafe impl crate::cp::FfiRecord for Net4 {}
// SAFETY: the native record contains only two byte arrays.
unsafe impl crate::cp::FfiRecord for Net6 {}
// SAFETY: both native interval bounds are integers.
unsafe impl crate::cp::FfiRecord for PortRange {}
// SAFETY: both native interval bounds are integers.
unsafe impl crate::cp::FfiRecord for LineRange {}
