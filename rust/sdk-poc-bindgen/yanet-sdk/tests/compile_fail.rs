//! Compile-fail suite of the layout derive and the export macro: bodies
//! with non-layout fields or without repr(C), hand-written layout impls in
//! a forbid(unsafe_code) crate, and an export under another module's name
//! must all be rejected.

use std::process::Command;

/// Compiler release the expected diagnostics were recorded with.
///
/// Other releases word the same errors differently (rustc 1.88 moves the
/// const-evaluation panic text into a label), so the suite runs only on
/// this one; the snapshots are refreshed with the workspace's stable bump.
const SNAPSHOT_RUSTC: &str = "rustc 1.98.";

/// Verifies that every source under compile_fail/ fails to build with the
/// recorded diagnostic.
#[test]
#[cfg_attr(miri, ignore = "invokes cargo")]
fn test_compile_fail_cases() {
    let rustc = std::env::var("RUSTC").unwrap_or_else(|_| "rustc".into());
    let version = Command::new(rustc).arg("--version").output().expect("run rustc");
    let version = String::from_utf8_lossy(&version.stdout);
    if !version.starts_with(SNAPSHOT_RUSTC) {
        eprintln!("skipped: diagnostics are recorded with {SNAPSHOT_RUSTC}x, this is {version}");
        return;
    }
    trybuild::TestCases::new().compile_fail("tests/compile_fail/*.rs");
}

// The library and its dependencies are used by the compiled cases.
use yanet_sdk as _;
use yanet_sdk_derive as _;
use yanet_sys as _;
