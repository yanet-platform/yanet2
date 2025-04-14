package bitset

import (
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

	bits := make([]int, 0)
	b.Traverse(func(idx int) {
		bits = append(bits, idx)
	})

	assert.Equal(t, []int{0, 42}, bits)
}

func Test_TinyBitsetTraverseEmpty(t *testing.T) {
	b := TinyBitset{}

	bits := make([]int, 0)
	b.Traverse(func(idx int) {
		bits = append(bits, idx)
	})

	assert.Equal(t, []int{}, bits)
}

func Test_TinyBitsetAsSlice(t *testing.T) {
	b := TinyBitset{}
	b.Insert(0)
	b.Insert(42)

	assert.Equal(t, []int{0, 42}, b.AsSlice())
}

func Test_TinyBitsetPanicsOnLargeIndex(t *testing.T) {
	b := TinyBitset{}

	assert.NotPanics(t, func() { b.Insert(0) })
	assert.NotPanics(t, func() { b.Insert(64*MaxBitsetWords - 1) })
	assert.Panics(t, func() { b.Insert(64 * MaxBitsetWords) })
}
