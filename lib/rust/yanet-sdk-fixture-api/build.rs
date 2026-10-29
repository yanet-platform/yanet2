fn main() {
    // The header carries the constants of the module crate.
    println!("cargo:rerun-if-changed=../yanet-sdk-fixture/src");
    yanet_sdk_build::generate_header(
        &["FixtureSpec", "FibDevice", "FibRoute", "FibList", "Range4", "Range6"],
        &["yanet-sdk-fixture"],
    );
}
