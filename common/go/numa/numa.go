package numa

import (
	"iter"
	"math/bits"

	"github.com/yanet-platform/yanet2/common/go/bitset"
)

const MAX = NUMAMap(^uint32(0))

type NUMAMap uint32

// NewWithOneBitSet returns a new NUMAMap with a single bit set at the
// specified index (zero-based).
//
// Panics if the idx >= 32.
func NewWithOneBitSet(idx uint32) NUMAMap {
	if idx >= 32 {
		panic("index is out of range")
	}

	return NUMAMap(1 << idx)
}

// NewWithTrailingOnes returns a new NUMAMap with the specified number of
// trailing ones.
func NewWithTrailingOnes(numOnes int) NUMAMap {
	if numOnes == 0 {
		return NUMAMap(0)
	}
	if numOnes > 32 {
		return MAX
	}

	return NUMAMap(^uint32(0) >> (32 - numOnes))
}

func (m NUMAMap) IsEmpty() bool {
	return m == 0
}

func (m NUMAMap) Len() int {
	return bits.OnesCount32(uint32(m))
}

func (m NUMAMap) Intersect(other NUMAMap) NUMAMap {
	return m & other
}

func (m NUMAMap) Iter() iter.Seq[uint32] {
	return bitset.NewBitsTraverser(uint64(m)).Iter()
}
