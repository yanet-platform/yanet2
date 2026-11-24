// Package shm reads and writes YANET's shared memory from Go without cgo.
//
// The C structures are reached through views generated from the DWARF of
// the dataplane's own compilation, so every field sits at the offset the C
// compiler chose. This package holds what those views need at run time:
// relative pointers, opaque process-local words, the spin and process-id
// locks C takes on shared memory, and the NUL-terminated strings of its
// character arrays.
package shm

import (
	"bytes"
	"strings"
	"sync/atomic"
	"unsafe"
)

// Ptr is a pointer as C stores it in shared memory: the distance from the
// pointer's own address to its target, zero meaning nil.
//
// Every process maps the storage at its own address, so a stored address
// would be wrong in every other process; a distance is right in all of
// them. Ptr is only ever used in place inside the mapping: a copy moved
// elsewhere points somewhere else, which go vet reports as a copied lock.
type Ptr[T any] struct {
	_      noCopy
	offset int64
}

// noCopy makes go vet report copies of the type that embeds it; it takes
// no space.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// Get returns the target, or nil.
func (m *Ptr[T]) Get() *T {
	return m.resolve(m.offset)
}

// Load returns the target with acquire ordering, for pointers C publishes
// with a release store.
func (m *Ptr[T]) Load() *T {
	return m.resolve(atomic.LoadInt64(&m.offset))
}

// Set points m at target, nil clearing it.
func (m *Ptr[T]) Set(target *T) {
	m.offset = m.distance(target)
}

// Store points m at target with release ordering, publishing everything
// written to the target before it.
func (m *Ptr[T]) Store(target *T) {
	atomic.StoreInt64(&m.offset, m.distance(target))
}

// IsNil reports whether m points nowhere.
func (m *Ptr[T]) IsNil() bool {
	return m.offset == 0
}

func (m *Ptr[T]) resolve(offset int64) *T {
	if offset == 0 {
		return nil
	}
	return (*T)(unsafe.Add(unsafe.Pointer(m), offset))
}

func (m *Ptr[T]) distance(target *T) int64 {
	if target == nil {
		return 0
	}
	return int64(uintptr(unsafe.Pointer(target)) - uintptr(unsafe.Pointer(m)))
}

// Local is a word only the process that wrote it can interpret: a function
// pointer or an absolute address the dataplane resolved for itself.
//
// A view keeps such a field at its size and offset and never follows it.
type Local uintptr

// CopyString copies value into a C character array as strtcpy does.
//
// It copies at most one byte short of the array, stopping at a NUL inside
// value, and writes a terminating NUL after it; the bytes past the
// terminator stay as they were. An empty array is left alone.
func CopyString(field []byte, value string) {
	if len(field) == 0 {
		return
	}
	if end := strings.IndexByte(value, 0); end >= 0 {
		value = value[:end]
	}
	length := min(len(value), len(field)-1)
	copy(field, value[:length])
	field[length] = 0
}

// String returns the NUL-terminated string a C character array holds.
func String(field []byte) string {
	text, _, _ := bytes.Cut(field, []byte{0})
	return string(text)
}
