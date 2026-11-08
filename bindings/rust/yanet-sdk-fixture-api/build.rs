//! Build script of the fixture api crate: the C header of its records and
//! constants, written when the build names a header directory.

fn main() {
    // The header carries the constants of the module crate.
    println!("cargo:rerun-if-changed=../yanet-sdk-fixture/src");
    yanet_sdk_build::generate_header(
        &[
            "FixtureSpec",
            "FibDevice",
            "FibRoute",
            "FibList",
            "StateShape",
            "Range4",
            "Range6",
            "Span",
            "AssignRule",
        ],
        &["yanet-sdk-fixture"],
    );
}
