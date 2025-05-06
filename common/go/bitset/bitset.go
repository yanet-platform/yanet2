package bitset

import (
	"fmt"
	"iter"
	"math/bits"
)

// MaxBitsetWords specifies the number of 64-bit words in the bitset.
const MaxBitsetWords = 16

// TinyBitset implements constant-length bitset.
//
// This structure is designed to be used as a comparable key in maps.
type TinyBitset struct {
	words [MaxBitsetWords]uint64
}

// Count returns the number of bits set in the bitset.
func (m *TinyBitset) Count() uint {
	count := uint(0)
	for _, word := range m.words {
		count += uint(bits.OnesCount64(word))
	}

	return count
}

// Insert inserts the given index into the bitset.
func (m *TinyBitset) Insert(idx uint32) {
	if idx >= 64*MaxBitsetWords {
		panic(fmt.Sprintf("index %d is too big: must be less than %d", idx, 64*MaxBitsetWords))
	}

	m.words[idx/64] |= 1 << (idx % 64)
}

// Traverse traverses the bitset and calls the given function for each bit set.
//
// Iteration is performed from the least significant bit to the most
// significant one.
func (m *TinyBitset) Traverse(fn func(uint32) bool) {
	for idx, word := range m.words {
		isContinue := NewBitsTraverser(word).Traverse(func(r uint32) bool {
			return fn(64*uint32(idx) + r)
		})

		if !isContinue {
			break
		}
	}
}

func (m *TinyBitset) Iter() iter.Seq[uint32] {
	return func(yield func(uint32) bool) {
		m.Traverse(yield)
	}
}

// AsSlice returns the bitset as a slice of indices, where each index is a
// position of the bit set.
func (m *TinyBitset) AsSlice() []uint32 {
	out := make([]uint32, 0, m.Count())

	m.Traverse(func(idx uint32) bool {
		out = append(out, idx)
		return true
	})

	return out
}

// BitsTraverser is an iterator that allows to iterate over all bits set in the
// given 64-bit unsigned integer.
//
// Iteration is performed from the least significant bit to the most
// significant one.
type BitsTraverser struct {
	word uint64
}

// NewBitsTraverser constructs a new bits traverser over given 64-bit word.
func NewBitsTraverser(word uint64) BitsTraverser {
	return BitsTraverser{word: word}
}

// Traverse traverses the bitset and calls the given function for each bit set.
func (m BitsTraverser) Traverse(fn func(uint32) bool) bool {
	word := m.word

	for word > 0 {
		r := bits.TrailingZeros64(word)
		// This produces an integer with only the least significant bit of the
		// word set, which is equivalent to "1 << r".
		//
		// But unlike bit shift, when combined with the following "xor"
		// operator, it compiles with a single "blsr" instruction, at least
		// on LLVM.
		//
		// Which makes this function ~60% faster.
		t := word & -word
		word ^= t

		if !fn(uint32(r)) {
			return false
		}
	}

	return true
}

// Iter returns an iterator over the bits set in this word.
func (m BitsTraverser) Iter() iter.Seq[uint32] {
	return func(yield func(uint32) bool) {
		m.Traverse(yield)
	}
}
