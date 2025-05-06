package bitset

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_TinyBitsetCount(t *testing.T) {
	b := TinyBitset{}

	assert.Equal(t, uint(0), b.Count())

	b.Insert(0)
	b.Insert(42)
	assert.Equal(t, uint(2), b.Count())
}

func Test_TinyBitsetTraverse(t *testing.T) {
	b := TinyBitset{}
	b.Insert(0)
	b.Insert(42)
	b.Insert(512)

	bits := make([]uint32, 0)
	b.Traverse(func(idx uint32) bool {
		bits = append(bits, idx)
		return true
	})

	assert.Equal(t, []uint32{0, 42, 512}, bits)
}

func Test_TinyBitsetPartialTraverse(t *testing.T) {
	b := TinyBitset{}
	b.Insert(42)
	b.Insert(84)
	b.Insert(512)

	bits := make([]uint32, 0)
	b.Traverse(func(idx uint32) bool {
		bits = append(bits, idx)
		return false
	})

	assert.Equal(t, []uint32{42}, bits)
}

func Test_TinyBitsetTraverseEmpty(t *testing.T) {
	b := TinyBitset{}

	bits := make([]uint32, 0)
	b.Traverse(func(idx uint32) bool {
		bits = append(bits, idx)
		return true
	})

	assert.Equal(t, []uint32{}, bits)
}

func Test_TinyBitsetIter(t *testing.T) {
	b := TinyBitset{}
	b.Insert(0)
	b.Insert(42)
	b.Insert(512)

	bits := slices.Collect(b.Iter())

	assert.Equal(t, []uint32{0, 42, 512}, bits)
}

func Test_TinyBitsetPartialIter(t *testing.T) {
	b := TinyBitset{}
	b.Insert(42)
	b.Insert(512)

	bits := make([]uint32, 0)
	for bit := range b.Iter() {
		bits = append(bits, bit)
		break
	}

	assert.Equal(t, []uint32{42}, bits)
}

func Test_TinyBitsetAsSlice(t *testing.T) {
	b := TinyBitset{}
	b.Insert(0)
	b.Insert(42)

	assert.Equal(t, []uint32{0, 42}, b.AsSlice())
}

func Test_TinyBitsetPanicsOnLargeIndex(t *testing.T) {
	b := TinyBitset{}

	assert.NotPanics(t, func() { b.Insert(0) })
	assert.NotPanics(t, func() { b.Insert(64*MaxBitsetWords - 1) })
	assert.Panics(t, func() { b.Insert(64 * MaxBitsetWords) })
}
