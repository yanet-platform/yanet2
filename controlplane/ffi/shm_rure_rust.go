//go:build yanet_rust_cp

package ffi

// With the Rust control-plane api the single Rust static library carries
// the rure C API too: linking librure.a as well would duplicate the Rust
// runtime symbols.

//#cgo LDFLAGS: ${SRCDIR}/../../rust/sdk-poc-bindgen/target/release/libyanet_cp.a
//#cgo LDFLAGS: -lgcc_s -lpthread -ldl -lm -lrt -lutil
import "C"
