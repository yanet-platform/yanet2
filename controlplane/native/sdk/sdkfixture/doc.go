// Package sdkfixture holds the Go views of the SDK fixture modules'
// configurations, which the build generates from their layout description.
//
// With the Rust fixtures enabled (meson option sdk_rust_fixtures), make
// native-layout runs the yanet-layout generator, which writes the generic
// shapes of the SDK field types and the views into zz_layout_<arch>.go, with
// a test against the same description in zz_layout_<arch>_test.go. Neither
// file is committed.
package sdkfixture
