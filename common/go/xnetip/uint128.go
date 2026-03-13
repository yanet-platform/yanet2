package xnetip

import (
	"encoding/binary"
	"math/bits"
)

// uint128 is a 128-bit unsigned integer stored as two 64-bit halves.
//
// NOTE: private for now, but can be made public if needed.
type uint128 struct {
	hi uint64
	lo uint64
}

// newUint128From16 constructs a uint128 from a 16-byte big-endian array.
func newUint128From16(b [16]byte) uint128 {
	return uint128{
		hi: binary.BigEndian.Uint64(b[:8]),
		lo: binary.BigEndian.Uint64(b[8:]),
	}
}

// Xor returns the bitwise XOR of two Uint128 values.
func (m uint128) Xor(other uint128) uint128 {
	return uint128{
		hi: m.hi ^ other.hi,
		lo: m.lo ^ other.lo,
	}
}

// LeadingZeros returns the number of leading zero bits.
func (m uint128) LeadingZeros() int {
	if m.hi != 0 {
		return bits.LeadingZeros64(m.hi)
	}
	return 64 + bits.LeadingZeros64(m.lo)
}
