//go:build linux && (amd64 || arm64)

// Package nativetest gives the native control plane's tests a dataplane to
// publish to.
//
// Only tests import it. The dataplane test harness runs in a helper
// process on storage the test maps; the plain build of the client needs no
// cgo, as the allocator helpers need none, while an AddressSanitizer build
// reaches the sanitizer's interface through it.
package nativetest
