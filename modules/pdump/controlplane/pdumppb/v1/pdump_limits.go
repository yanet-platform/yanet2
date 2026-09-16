//go:build !yanet_asan

package pdumppb

// MaxRingSize mirrors the allocator's usable limit for a per-worker ring
// buffer without sanitizer red zones.
const MaxRingSize = 1 << 26
