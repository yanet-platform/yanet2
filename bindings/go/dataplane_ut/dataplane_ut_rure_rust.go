//go:build yanet_rust_cp

package dataplaneut

// The Rust control-plane library carries the rure C API in this build.

//#cgo LDFLAGS: ${SRCDIR}/../../../rust/sdk-poc-bindgen/target/release/libyanet_cp.a
//#cgo LDFLAGS: -lgcc_s -lpthread -ldl -lm -lrt -lutil
import "C"
