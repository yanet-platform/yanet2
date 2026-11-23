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

// The ready magic's parts: its high word marks a ready instance, bits
// 16-31 carry the module ABI version and the low 16 bits a layout counter.
const (
	readyMarkShift = 32
	abiShift       = 16
	versionMask    = 0xffff
)

// ErrNotReady reports a storage whose dataplane has not marked an
// instance ready.
var ErrNotReady = errors.New("dataplane instance is not ready")

// ErrVersionMismatch reports a ready instance whose dataplane was built
// with another module ABI version or another shared-memory layout than
// this process.
var ErrVersionMismatch = errors.New("dataplane shared memory version differs")

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
	// memory is the instance's whole storage extent.
	memory []byte
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
// A second call does nothing.
func (m *Storage) Close() error {
	m.Instances = nil
	data := m.data
	m.data = nil
	if m.borrowed || data == nil {
		return nil
	}
	return syscall.Munmap(data)
}

// Contains reports whether size bytes at address lie inside the mapping.
func (m *Storage) Contains(address unsafe.Pointer, size uintptr) bool {
	return contains(m.data, address, size)
}

// contains reports whether size bytes at address lie inside memory.
func contains(memory []byte, address unsafe.Pointer, size uintptr) bool {
	if len(memory) == 0 {
		return false
	}
	start := uintptr(unsafe.Pointer(&memory[0]))
	at := uintptr(address)
	return at >= start && size <= uintptr(len(memory)) && at-start <= uintptr(len(memory))-size
}

// findInstances walks the instances the first one counts: each starts
// where the previous one's storage ends.
func (m *Storage) findInstances() error {
	base := unsafe.Pointer(&m.data[0])
	first := (*layout.DpConfig)(base)
	if err := checkMagic(0, atomic.LoadUint64(&first.ReadyMagic)); err != nil {
		return err
	}
	count := atomic.LoadUint32(&first.InstanceCount)
	if count == 0 {
		return errors.New("the dataplane declares no instances")
	}
	offset := uintptr(0)
	for idx := range int(count) {
		address := unsafe.Add(base, offset)
		if !m.Contains(address, unsafe.Sizeof(layout.DpConfig{})) {
			return fmt.Errorf("instance %d at offset %d lies outside the storage", idx, offset)
		}
		dp := (*layout.DpConfig)(address)
		if err := checkMagic(idx, atomic.LoadUint64(&dp.ReadyMagic)); err != nil {
			return err
		}
		cp := dp.CpConfig.Get()
		if cp == nil || !m.Contains(unsafe.Pointer(cp), unsafe.Sizeof(layout.CpConfig{})) {
			return fmt.Errorf("instance %d: control-plane zone outside the storage", idx)
		}
		if dp.StorageSize == 0 {
			return fmt.Errorf("instance %d: zero storage size", idx)
		}
		// Every pointer read later lands inside the instance, so the whole
		// extent must be mapped; it also keeps the next offset in range.
		if !m.Contains(address, uintptr(dp.StorageSize)) {
			return fmt.Errorf("instance %d: storage of %d bytes at offset %d runs past the mapping", idx, dp.StorageSize, offset)
		}
		m.Instances = append(m.Instances, &Instance{
			Index:  idx,
			Dp:     dp,
			Cp:     cp,
			memory: unsafe.Slice((*byte)(address), dp.StorageSize),
		})
		offset += uintptr(dp.StorageSize)
	}
	return nil
}

// checkMagic accepts an instance's ready magic only if it equals the one
// this build was generated with.
//
// A magic without the ready mark is an instance not ready yet; a ready one
// that differs names the module ABI versions when they differ, else the
// layout counters.
func checkMagic(instance int, magic uint64) error {
	want := layout.DpConfigReadyMagic
	switch {
	case magic == want:
		return nil
	case magic>>readyMarkShift != want>>readyMarkShift:
		return fmt.Errorf("%w: instance %d magic %#x", ErrNotReady, instance, magic)
	case magic>>abiShift&versionMask != uint64(layout.ModuleABIVersion):
		return fmt.Errorf(
			"%w: instance %d: dataplane module ABI version %d, this build reads %d",
			ErrVersionMismatch, instance, magic>>abiShift&versionMask, layout.ModuleABIVersion,
		)
	default:
		return fmt.Errorf(
			"%w: instance %d: dataplane layout counter %d, this build reads %d",
			ErrVersionMismatch, instance, magic&versionMask, want&versionMask,
		)
	}
}
