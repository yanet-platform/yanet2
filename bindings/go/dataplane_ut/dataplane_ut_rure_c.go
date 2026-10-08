//go:build !yanet_rust_cp

package dataplaneut

// The counter pattern library calls the rure C API, which the default
// build takes from the meson-built librure.a.

//#cgo LDFLAGS: -L../../../build/lib/counters -lrure
import "C"
