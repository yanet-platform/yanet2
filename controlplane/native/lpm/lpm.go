//go:build linux && (amd64 || arm64)

// Package lpm builds the longest-prefix-match trees of common/lpm.h in
// shared memory, for the dataplane's lookup to read.
//
// A tree maps ranges of big-endian keys to values through pages of 256
// slots, one page per key byte; a slot holds a value or its child page.
// The pages, their chunk directory and the order the tree grows in are
// C's, so a tree built here holds the bytes C builds from the same
// inserts.
package lpm

import (
	"bytes"
	"fmt"
	"math"
	"math/bits"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// chunkSize is the number of pages allocated together
	// (LPM_CHUNK_SIZE).
	chunkSize = 16
	// valueFlag marks a slot holding a value rather than a child page
	// (LPM_VALUE_FLAG).
	valueFlag = 1
	// maxKeySize is the longest key, in bytes, whose size C's functions
	// take, as one byte.
	maxKeySize = math.MaxUint8
)

// errTreeMemory reports that the memory of a tree's context ran out.
var errTreeMemory = fmt.Errorf("prefix tree memory: %w", shmalloc.ErrNoMemory)

// slotValue views a slot as the tagged value C reads through the union.
func slotValue(slot *layout.LpmValue) *uint64 {
	return (*uint64)(unsafe.Pointer(slot))
}

// slotPage views a slot as the relative pointer to its child page.
func slotPage(slot *layout.LpmValue) *shm.Ptr[layout.LpmPage] {
	return (*shm.Ptr[layout.LpmPage])(unsafe.Pointer(slot))
}

// chunkCapacity returns the number of entries the chunk directory holds
// for a chunk count, as lpm_chunk_capacity does.
//
// The directory grows to the next power of two; the capacity follows
// from the page count alone, so growth and free agree on its size.
func chunkCapacity(chunkCount uint64) uint64 {
	if chunkCount == 0 {
		return 0
	}
	return 1 << bits.Len64(chunkCount-1)
}

// page returns the page of that index, as lpm_page does.
func page(tree *layout.Lpm, index uint64) *layout.LpmPage {
	directory := registry.Slice(tree.Pages.Get(), index/chunkSize+1)
	chunk := registry.Slice(directory[index/chunkSize].Get(), chunkSize)
	return &chunk[index%chunkSize]
}

// newPage appends a page to the tree, as lpm_new_page does.
//
// The first page of a chunk allocates the chunk, after growing a full
// chunk directory; a failure frees what the call allocated. A slot given
// turns from a value into the new page, which takes that value in every
// slot so the broader range is kept; without one the page holds invalid
// values only.
func newPage(tree *layout.Lpm, slot *layout.LpmValue) error {
	if tree.PageCount%chunkSize == 0 {
		oldChunkCount := tree.PageCount / chunkSize
		oldCapacity := chunkCapacity(oldChunkCount)
		grow := oldChunkCount == oldCapacity
		newCapacity := chunkCapacity(oldChunkCount + 1)

		context := &tree.MemoryContext
		oldPages := registry.Slice(tree.Pages.Get(), oldCapacity)
		pages := oldPages
		if grow {
			var ok bool
			if pages, ok = registry.AllocArray[shm.Ptr[layout.LpmPage]](context, newCapacity); !ok {
				return errTreeMemory
			}
		}

		chunk, ok := registry.AllocArray[layout.LpmPage](context, chunkSize)
		if !ok {
			if grow {
				registry.FreeArray(context, pages)
			}
			return errTreeMemory
		}

		pages[oldChunkCount].Set(&chunk[0])

		if grow {
			for idx := range oldChunkCount {
				pages[idx].Set(oldPages[idx].Get())
			}
			tree.Pages.Set(&pages[0])
			registry.FreeArray(context, oldPages)
		}
	}
	fresh := page(tree, tree.PageCount)
	tree.PageCount++

	if slot != nil {
		fill(fresh, *slotValue(slot))
		slotPage(slot).Set(page(tree, tree.PageCount-1))
		return nil
	}
	fill(fresh, ^uint64(0))
	return nil
}

// fill writes the value into every slot of a page.
//
// It writes a run of slots on the stack and copies the run across the
// page: a copy stores whole vector registers where a loop over the slots
// stores a word at a time, and copying from the page itself would wait
// for its lines to arrive from memory.
func fill(fresh *layout.LpmPage, value uint64) {
	var run [32]layout.LpmValue
	for idx := range run {
		*slotValue(&run[idx]) = value
	}
	for done := 0; done < len(fresh.Values); done += len(run) {
		copy(fresh.Values[done:], run[:])
	}
}

// Init prepares an empty tree of one page whose memory context is a
// child of the parent context, as lpm_init does.
//
// A failure leaves the tree's context linked under the parent and holding
// nothing, for the free to unlink.
func Init(tree *layout.Lpm, parent *layout.MemoryContext, name string) error {
	shmalloc.ContextInitFrom(&tree.MemoryContext, parent, name)
	tree.Pages.Set(nil)
	tree.PageCount = 0
	return newPage(tree, nil)
}

// insertRange is a range insert as every page of its walk reads it.
type insertRange struct {
	Tree *layout.Lpm
	From []byte
	To   []byte
	// FromLen is the length of the start up to its last nonzero byte,
	// 0 if none.
	FromLen int
	// ToLen is the length of the end up to its last byte other than
	// 0xff, 0 if none.
	ToLen int
	Leaf  uint64
}

// insertPage writes the value into the slots of one page the range
// covers, in key order, as lpm_insert_page does.
//
// The flags tell whether the key prefix leading to the page equals the
// prefix of the range start and of the range end: only then does the page
// bound the walk, and only its slot on that boundary can cover a subtree
// in part. Such a slot splits its leaf into a page of the old value and
// the walk goes down; any other leaf takes the value. An existing child
// page is always walked and never collapsed, so the tree only grows.
func insertPage(insert *insertRange, current *layout.LpmPage, hop int, onFrom, onTo bool) error {
	first, last := 0x00, 0xff
	if onFrom {
		first = int(insert.From[hop])
	}
	if onTo {
		last = int(insert.To[hop])
	}
	lastHop := hop+1 == len(insert.From)

	for key := first; key <= last; key++ {
		slot := &current.Values[key]
		keyFrom := onFrom && key == first
		keyTo := onTo && key == last

		if *slotValue(slot)&valueFlag != 0 {
			partial := !lastHop &&
				((keyFrom && insert.FromLen > hop+1) || (keyTo && insert.ToLen > hop+1))
			if !partial {
				*slotValue(slot) = insert.Leaf
				continue
			}
			if err := newPage(insert.Tree, slot); err != nil {
				return err
			}
		}

		if err := insertPage(insert, slotPage(slot).Get(), hop+1, keyFrom, keyTo); err != nil {
			return err
		}
	}
	return nil
}

// Insert maps the big-endian keys from the start through the end, both
// included, to the value, as lpm_insert does.
//
// Both keys are as long as every key of the tree; keys of no or differing
// sizes, or longer than C can size, are refused before the tree is
// touched. A start past the end maps
// nothing. A later insert overwrites the keys it covers. A failure leaves
// a tree the lookup and the free still read, with part of the range
// mapped.
func Insert(tree *layout.Lpm, from, to []byte, value uint32) error {
	if len(from) == 0 || len(from) > maxKeySize || len(from) != len(to) {
		return fmt.Errorf("prefix tree keys must be of one size from 1 to %d bytes", maxKeySize)
	}
	if bytes.Compare(from, to) > 0 {
		return nil
	}

	insert := &insertRange{
		Tree: tree,
		From: from,
		To:   to,
		Leaf: uint64(value)<<1 | valueFlag,
	}
	for length := len(from); length > 0; length-- {
		if insert.FromLen == 0 && from[length-1] != 0x00 {
			insert.FromLen = length
		}
		if insert.ToLen == 0 && to[length-1] != 0xff {
			insert.ToLen = length
		}
	}
	return insertPage(insert, page(tree, 0), 0, true, true)
}
