//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture

import (
	"bytes"
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
)

// Range4 is an inclusive IPv4 range of a prefix tree and its value.
type Range4 struct {
	From, To [4]byte
	Value    uint32
}

// Range6 is an inclusive IPv6 range of a prefix tree and its value.
type Range6 struct {
	From, To [16]byte
	Value    uint32
}

// Bounds returns the range's first and last keys and its value.
func (m Range4) Bounds() (from, to []byte, value uint32) {
	return m.From[:], m.To[:], m.Value
}

// Bounds returns the range's first and last keys and its value.
func (m Range6) Bounds() (from, to []byte, value uint32) {
	return m.From[:], m.To[:], m.Value
}

// lpmRange is a range of a prefix tree's keys.
type lpmRange interface {
	Range4 | Range6
	Bounds() (from, to []byte, value uint32)
}

// checkRanges refuses a range that ends before it starts, which the
// tree's insert would read as mapping nothing.
func checkRanges[R lpmRange](ranges []R) error {
	for idx, item := range ranges {
		if from, to, _ := item.Bounds(); bytes.Compare(from, to) > 0 {
			return fmt.Errorf("range %d ends before it starts: %w", idx, ErrInvalidArgument)
		}
	}
	return nil
}

// buildLpm builds the prefix tree of the ranges, inserted in order, so
// that the later one wins on an overlap, as the SDK builds an LPM field.
//
// No ranges leave the tree zeroed, holding no memory context. A failure
// frees what the tree took and leaves it zeroed.
func buildLpm[R lpmRange](tree *layout.Lpm, parent *layout.MemoryContext, ranges []R) error {
	*tree = layout.Lpm{}
	if len(ranges) == 0 {
		return nil
	}
	err := lpm.Init(tree, parent, "lpm")
	for _, item := range ranges {
		if err != nil {
			break
		}
		from, to, value := item.Bounds()
		err = lpm.Insert(tree, from, to, value)
	}
	if err != nil {
		lpm.Free(tree)
		*tree = layout.Lpm{}
	}
	return err
}

// freeLpm frees a prefix tree buildLpm built; a zeroed one holds nothing.
func freeLpm(tree *layout.Lpm) {
	if tree.Pages.Get() != nil {
		lpm.Free(tree)
	}
}
