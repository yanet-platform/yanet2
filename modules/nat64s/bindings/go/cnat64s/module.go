package cnat64s

/*
#cgo CFLAGS: -I../../../../../

#include "api/agent.h"
#include "lib/fwstate/nat64state.h"
#include "modules/nat64s/api/controlplane.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// PrefixLen is the length in bytes of a NAT64 prefix (/96).
const PrefixLen = 12

// ModuleConfig is an opaque handle to a nat64s module configuration in
// shared memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// ModuleUpdate is the whole replaceable state of one nat64s module
// config, mapping 1:1 to struct nat64s_module_update.
type ModuleUpdate struct {
	// Prefixes are the 12-byte NAT64 prefixes for v4->v6 destinations;
	// nil leaves the config with none.
	Prefixes [][]byte
	// Pool is the public IPv4 pool in network byte order.
	Pool []uint32
	// Timeout carries the two session timeouts; zero knobs select
	// their defaults.
	Timeout SessionTimeout
	MTUv4   uint16
	MTUv6   uint16
	// PortLo and PortHi bound the translated source-port window
	// [PortLo, PortHi); an empty window selects the default.
	PortLo uint16
	PortHi uint16
	// MapName names the linked nat64state_map object; empty links none.
	MapName string
}

// SessionTimeout carries the two session timeouts in seconds: one for
// every session, and a longer one for a TCP session that has carried an
// ACK.
type SessionTimeout struct {
	Timeout        uint16
	TCPEstablished uint16
}

// NewModuleConfig allocates a new, empty nat64s module configuration.
//
// The caller owns it and releases it with Free while its agent is alive:
// the config lives in shared memory, which nothing releases on the
// owner's behalf once the handle is dropped.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.nat64s_module_config_create(
		(*C.struct_agent)(agent.AsRawPtr()), cName, &cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to initialize module config: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	config := &ModuleConfig{ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr))}
	return config, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

// AsFFIModule returns the underlying common module config handle.
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// Update applies the whole update to the config.
func (m *ModuleConfig) Update(update ModuleUpdate) error {
	for idx, prefix := range update.Prefixes {
		if len(prefix) != PrefixLen {
			return fmt.Errorf(
				"prefixes[%d] must be %d bytes, got %d",
				idx, PrefixLen, len(prefix),
			)
		}
	}

	// The C side reads the name and the two arrays during its call, so
	// their storage is pinned for exactly that long: the update struct
	// carries Go pointers into C.
	var pinner runtime.Pinner
	defer pinner.Unpin()

	cMapName := C.CString(update.MapName)
	defer C.free(unsafe.Pointer(cMapName))

	cUpdate := C.struct_nat64s_module_update{
		map_name: cMapName,
		timeouts: C.struct_nat64state_timeouts{
			timeout:         C.uint16_t(update.Timeout.Timeout),
			tcp_established: C.uint16_t(update.Timeout.TCPEstablished),
		},
		mtu_ipv4: C.uint16_t(update.MTUv4),
		mtu_ipv6: C.uint16_t(update.MTUv6),
		port_lo:  C.uint16_t(update.PortLo),
		port_hi:  C.uint16_t(update.PortHi),
	}
	if len(update.Prefixes) != 0 {
		// One packed [N][12] buffer the C side reads as a list.
		packed := make([]byte, 0, len(update.Prefixes)*PrefixLen)
		for _, prefix := range update.Prefixes {
			packed = append(packed, prefix...)
		}
		pinner.Pin(&packed[0])
		cUpdate.prefixes = (*C.uint8_t)(unsafe.Pointer(&packed[0]))
		cUpdate.prefix_count = C.uint64_t(len(update.Prefixes))
	}
	if len(update.Pool) != 0 {
		pinner.Pin(&update.Pool[0])
		cUpdate.pool = (*C.uint32_t)(unsafe.Pointer(&update.Pool[0]))
		cUpdate.pool_count = C.uint64_t(len(update.Pool))
	}

	var cErr *C.yanet_error
	rc := C.nat64s_module_config_update(m.asRawPtr(), &cUpdate, &cErr)
	if rc != 0 {
		return fmt.Errorf(
			"failed to update module config: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

// Free destroys the module config, or reports ffi.ErrStillReferenced
// while a live generation still holds it. Safe to call multiple times.
func (m *ModuleConfig) Free() error {
	return m.ptr.Free(func(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cErr *C.yanet_error
		rc, errno := C.nat64s_module_config_free((*C.struct_cp_module)(ptr), &cErr)
		return int(rc), unsafe.Pointer(cErr), errno
	})
}
