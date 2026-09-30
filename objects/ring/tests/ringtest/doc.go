// Package ringtest holds CGo test helpers for the ring object.
//
// The helpers reach into the C ring and the config registry in ways no
// production code does. This lets tests control timing and state exactly.
//
//   - Writer runs the C ring writer on one worker's ring, so a test decides
//     when records are committed and evicted. Stress runs it on its own OS
//     thread.
//   - Hold adds an extra reference to a published ring. A test uses it to
//     make a free fail without waiting on generation timing.
//   - LinkRing links a module config to a named ring. A test uses it to
//     check that a linked ring cannot be deleted.
//
// Writer and Stress call the C library built by meson next to the ring
// object archive. They never read the ring metadata from Go, so they do
// not depend on the YANET_CACHE_LINE_SIZE that cgo builds this package
// with. Hold does: its inline code reads a config field that sits after the
// config lock, and that lock is padded to a cache line. So Hold needs the
// same value as the C archives.
//
// Only tests import this package.
package ringtest
