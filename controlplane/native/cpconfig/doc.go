//go:build linux && (amd64 || arm64)

// Package cpconfig builds, publishes, deletes and frees YANET module
// configurations and objects from Go without cgo, as
// lib/controlplane/config does.
//
// Configurations, the generations that publish them, every worker's
// execution contexts and counter storages are laid out and hashed as C
// does, so C reads, carries on and frees what Go builds. A failed build
// frees what it built as C's error paths do, with the frees teardown
// uses, and publishes nothing; the configurations it linked stay linked,
// as C leaves them. Locks follow C's order: the configuration lock, the
// agent lock briefly, then the allocators' locks, each taken by its own
// allocation or free, so none is held while another is taken.
package cpconfig
