//go:build yanet_asan

package pdumppb

// MaxRingSize mirrors the allocator's usable limit for a per-worker ring
// buffer after reserving two 64-byte address-sanitizer red zones.
const MaxRingSize = (1 << 26) - 2*64
