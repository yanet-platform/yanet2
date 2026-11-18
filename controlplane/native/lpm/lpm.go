// Package lpm builds the longest-prefix-match trees of common/lpm.h in
// shared memory, without cgo, for the dataplane's own lookup to read: the
// pages, their chunk directory and the order the tree grows in are C's.
package lpm

import (
	"bytes"
	"errors"
	"math/bits"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// chunkSize is the number of pages allocated together.
	chunkSize = 16
	// valueFlag marks a slot holding a value rather than a child page.
	valueFlag = 1
)

// ErrNoMemory reports that the tree's memory is exhausted.
var ErrNoMemory = errors.New("lpm memory exhausted")

// slotValue returns a slot as the tagged value C reads through the union.
func slotValue(slot *layout.LpmValue) *uint64 {
	return (*uint64)(unsafe.Pointer(slot))
}

// slotPage returns a slot as the relative pointer to its child page.
func slotPage(slot *layout.LpmValue) *shm.Ptr[layout.LpmPage] {
	return (*shm.Ptr[layout.LpmPage])(unsafe.Pointer(slot))
}

func chunkCapacity(chunks uint64) uint64 {
	switch {
	case chunks == 0:
		return 0
	case chunks == 1:
		return 1
	default:
		return 1 << bits.Len64(chunks-1)
	}
}

func chunks(tree *layout.Lpm, count uint64) []shm.Ptr[layout.LpmPage] {
	return unsafe.Slice(tree.Pages.Get(), count)
}

func page(tree *layout.Lpm, index uint64) *layout.LpmPage {
	chunk := chunks(tree, index/chunkSize+1)[index/chunkSize].Get()
	return &unsafe.Slice(chunk, chunkSize)[index%chunkSize]
}

// newPage appends a page; a slot passed in turns from a value into the
// page, which inherits the value in every slot.
func newPage(tree *layout.Lpm, slot *layout.LpmValue) error {
	context := &tree.MemoryContext
	pointerSize := unsafe.Sizeof(shm.Ptr[layout.LpmPage]{})
	if tree.PageCount%chunkSize == 0 {
		oldChunks := tree.PageCount / chunkSize
		oldCapacity := chunkCapacity(oldChunks)
		grow := oldChunks == oldCapacity
		capacity := chunkCapacity(oldChunks + 1)
		directory := tree.Pages.Get()
		if grow {
			grown := shmalloc.ContextAlloc(context, pointerSize*uintptr(capacity))
			if grown == nil {
				return ErrNoMemory
			}
			directory = (*shm.Ptr[layout.LpmPage])(grown)
		}
		chunk := shmalloc.ContextAlloc(context, unsafe.Sizeof(layout.LpmPage{})*chunkSize)
		if chunk == nil {
			if grow {
				shmalloc.ContextFree(context, unsafe.Pointer(directory), pointerSize*uintptr(capacity))
			}
			return ErrNoMemory
		}
		entries := unsafe.Slice(directory, capacity)
		entries[oldChunks].Set((*layout.LpmPage)(chunk))
		if grow {
			old := tree.Pages.Get()
			if old != nil {
				previous := unsafe.Slice(old, oldCapacity)
				for idx := range oldChunks {
					entries[idx].Set(previous[idx].Get())
				}
			}
			tree.Pages.Set(directory)
			if old != nil {
				shmalloc.ContextFree(context, unsafe.Pointer(old), pointerSize*uintptr(oldCapacity))
			}
		}
	}
	fresh := page(tree, tree.PageCount)
	tree.PageCount++
	if slot != nil {
		inherited := *slotValue(slot)
		for idx := range fresh.Values {
			*slotValue(&fresh.Values[idx]) = inherited
		}
		slotPage(slot).Set(page(tree, tree.PageCount-1))
		return nil
	}
	for idx := range fresh.Values {
		*slotValue(&fresh.Values[idx]) = ^uint64(0)
	}
	return nil
}

// Init prepares an empty tree whose memory context is a child of parent.
func Init(tree *layout.Lpm, parent *layout.MemoryContext, name string) error {
	shmalloc.ContextInitFrom(&tree.MemoryContext, parent, name)
	tree.Pages.Set(nil)
	tree.PageCount = 0
	return newPage(tree, nil)
}

// insertRange is a range insert as every page of its walk reads it.
type insertRange struct {
	Tree    *layout.Lpm
	From    []byte
	To      []byte
	FromLen int
	ToLen   int
	Leaf    uint64
}

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
			partial := !lastHop && ((keyFrom && insert.FromLen > hop+1) || (keyTo && insert.ToLen > hop+1))
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

// Insert maps the big-endian keys from through to, both included, to
// value; a later insert overwrites the keys it covers. from and to have
// the tree's key size.
func Insert(tree *layout.Lpm, from, to []byte, value uint32) error {
	if len(from) == 0 || len(from) != len(to) {
		return errors.New("keys must be non-empty and of one size")
	}
	if bytes.Compare(from, to) > 0 {
		return nil
	}
	insert := &insertRange{Tree: tree, From: from, To: to, Leaf: uint64(value)<<1 | valueFlag}
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
