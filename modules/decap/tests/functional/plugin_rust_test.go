//go:build yanet_rust_cp

package decap_test

// decapPluginDir holds the Rust decap module, which the plugin loader
// prefers over the built-in one: configurations from the Rust api have the
// SDK layout only the Rust module reads.
//
// Build it first with `cargo build --release -p decap-rs` in
// rust/sdk-poc-bindgen.
const decapPluginDir = "../../../../rust/sdk-poc-bindgen/target/release"
