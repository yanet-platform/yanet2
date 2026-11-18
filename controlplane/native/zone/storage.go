//go:build linux && (amd64 || arm64)

// Package zone attaches to YANET's shared memory without cgo: it maps the
// storage file a dataplane created, finds its instances and reads the
// configuration they publish.
package zone

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// ReadyMagic is the value a dataplane stores in an instance once it is
// ready; it also versions the shared-memory layout, so a storage built
// with another layout refuses the attach (DP_CONFIG_READY_MAGIC).
const ReadyMagic uint64 = 0xDEAD10CC0000000E

// ErrNotReady reports a storage whose dataplane has not marked an
// instance ready, or marked it with the magic of another layout.
var ErrNotReady = errors.New("dataplane instance is not ready")

// Storage is the storage file of one dataplane mapped into this process.
type Storage struct {
	data []byte
	// borrowed marks memory this package did not map.
	borrowed bool
	// Instances holds the dataplane instances, one per NUMA node.
	Instances []*Instance
}

// Instance is one dataplane instance: its dataplane zone and the
// control-plane zone that follows it.
type Instance struct {
	Index int
	Dp    *layout.DpConfig
	Cp    *layout.CpConfig
}

// Attach maps the storage file at path and finds its ready instances.
func Attach(path string) (*Storage, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < int64(unsafe.Sizeof(layout.DpConfig{})) {
		return nil, fmt.Errorf("%s: %d bytes cannot hold a dataplane zone", path, info.Size())
	}
	data, err := syscall.Mmap(
		int(file.Fd()), 0, int(info.Size()),
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED,
	)
	if err != nil {
		return nil, fmt.Errorf("map %s: %w", path, err)
	}
	m := &Storage{data: data}
	if err := m.findInstances(); err != nil {
		_ = syscall.Munmap(data)
		return nil, err
	}
	return m, nil
}

// FromMemory reads a storage that is already in this process's memory,
// such as the arena of an in-process dataplane. The caller keeps the
// memory alive and owns it; Close leaves it alone.
func FromMemory(data []byte) (*Storage, error) {
	if uintptr(len(data)) < unsafe.Sizeof(layout.DpConfig{}) {
		return nil, fmt.Errorf("%d bytes cannot hold a dataplane zone", len(data))
	}
	m := &Storage{data: data, borrowed: true}
	if err := m.findInstances(); err != nil {
		return nil, err
	}
	return m, nil
}

// Close unmaps the storage; nothing read from it may be used afterwards.
func (m *Storage) Close() error {
	m.Instances = nil
	if m.borrowed {
		return nil
	}
	return syscall.Munmap(m.data)
}

// Contains reports whether size bytes at address lie inside the mapping.
func (m *Storage) Contains(address unsafe.Pointer, size uintptr) bool {
	start := uintptr(unsafe.Pointer(&m.data[0]))
	at := uintptr(address)
	return at >= start && size <= uintptr(len(m.data)) && at-start <= uintptr(len(m.data))-size
}

// findInstances walks the instances the first one counts: each starts
// where the previous one's storage ends.
func (m *Storage) findInstances() error {
	base := unsafe.Pointer(&m.data[0])
	first := (*layout.DpConfig)(base)
	if magic := atomic.LoadUint64(&first.ReadyMagic); magic != ReadyMagic {
		return fmt.Errorf("%w: magic %#x, this build reads %#x", ErrNotReady, magic, ReadyMagic)
	}
	count := atomic.LoadUint32(&first.InstanceCount)
	offset := uintptr(0)
	for idx := range int(count) {
		address := unsafe.Add(base, offset)
		if !m.Contains(address, unsafe.Sizeof(layout.DpConfig{})) {
			return fmt.Errorf("instance %d at offset %d lies outside the storage", idx, offset)
		}
		dp := (*layout.DpConfig)(address)
		if magic := atomic.LoadUint64(&dp.ReadyMagic); magic != ReadyMagic {
			return fmt.Errorf("%w: instance %d magic %#x", ErrNotReady, idx, magic)
		}
		cp := dp.CpConfig.Get()
		if cp == nil || !m.Contains(unsafe.Pointer(cp), unsafe.Sizeof(layout.CpConfig{})) {
			return fmt.Errorf("instance %d: control-plane zone outside the storage", idx)
		}
		m.Instances = append(m.Instances, &Instance{Index: idx, Dp: dp, Cp: cp})
		if dp.StorageSize == 0 {
			return fmt.Errorf("instance %d: zero storage size", idx)
		}
		offset += uintptr(dp.StorageSize)
	}
	return nil
}
