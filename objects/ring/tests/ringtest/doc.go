// Package ringtest holds CGo test helpers for the ring object.
//
// They drive the C ring writer, hold extra references and link module
// configs to rings. Their C code is built by meson with the ring object
// archive's cache line size. Only tests import this package.
package ringtest
