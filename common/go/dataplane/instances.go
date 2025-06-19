package dataplane

import (
	"iter"
	"math/bits"

	"github.com/yanet-platform/yanet2/common/go/bitset"
)

const MAX = DpInstanceMap(^uint32(0))

// Bit mask of data plane instances.
type DpInstanceMap uint32

// NewWithOneBitSet returns a new instance map with a single bit set at the
// specified index (zero-based).
//
// Panics if the idx >= 32.
func NewWithOneBitSet(idx uint32) DpInstanceMap {
	if idx >= 32 {
		panic("index is out of range")
	}

	return DpInstanceMap(1 << idx)
}

// NewWithTrailingOnes returns a new instance map with the specified number of
// trailing ones.
func NewWithTrailingOnes(numOnes int) DpInstanceMap {
	if numOnes == 0 {
		return DpInstanceMap(0)
	}
	if numOnes > 32 {
		return MAX
	}

	return DpInstanceMap(^uint32(0) >> (32 - numOnes))
}

func (m DpInstanceMap) IsEmpty() bool {
	return m == 0
}

func (m DpInstanceMap) Len() int {
	return bits.OnesCount32(uint32(m))
}

func (m DpInstanceMap) Intersect(other DpInstanceMap) DpInstanceMap {
	return m & other
}

func (m DpInstanceMap) Iter() iter.Seq[uint32] {
	return bitset.NewBitsTraverser(uint64(m)).Iter()
}

func (m *DpInstanceMap) Enable(i uint32) {
	*m |= 1 << i
}
