//! Compile-fail suite: the export macro grammar and the forbidden-unsafe
//! module rule must reject these sources.

// The library is used by the compiled cases, not by this harness.
use yanet_sys as _;

/// Verifies that every source under compile_fail/ fails to build with the
/// recorded diagnostic.
#[test]
#[cfg_attr(miri, ignore = "invokes cargo")]
fn test_compile_fail_cases() {
    trybuild::TestCases::new().compile_fail("tests/compile_fail/*.rs");
}
