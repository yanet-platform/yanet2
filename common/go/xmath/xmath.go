package xmath

import "math/bits"

// NextPowerOfTwo32 returns the smallest power of two not less than the input.
//
// It returns zero when the result does not fit in 32 bits.
func NextPowerOfTwo32(value uint32) uint32 {
	if value <= 1 {
		return 1
	}
	if value > 1<<31 {
		return 0
	}
	return uint32(1) << bits.Len32(value-1)
}
