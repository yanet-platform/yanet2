package cfwstate

//#include <stdlib.h>
//#include "common/container_of.h"
//#include "common/memory.h"
//#include "lib/fwstate/config.h"
//#include "lib/fwstate/fwstate_cursor.h"
//#include "modules/fwstate/api/fwstate_cp.h"
//#include "modules/fwstate/dataplane/config.h"
import "C"

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// TTL48Max is the largest TTL (ns) storable in fw_state_value::last_ttl.
const TTL48Max = uint64(C.FWSTATE_TTL48_MAX)

// maxCursorBatch caps the allocation made by readEntries regardless of the
// caller-supplied count, providing a defence-in-depth limit at the binding layer.
const maxCursorBatch uint32 = 10000

// SyncConfig stores fwstate synchronization settings for C API calls.
type SyncConfig struct {
	SrcAddr          [16]byte
	DstEther         [6]byte
	DstAddrMulticast [16]byte
	DstAddrUnicast   [16]byte
	PortMulticast    uint16
	PortUnicast      uint16
	TcpSynAck        uint64
	TcpSyn           uint64
	TcpFin           uint64
	Tcp              uint64
	Udp              uint64
	Default          uint64
}

func newSyncConfigFromC(cCfg *C.struct_fwstate_sync_config) SyncConfig {
	var syncCfg SyncConfig
	copy(syncCfg.SrcAddr[:], unsafe.Slice((*byte)(unsafe.Pointer(&cCfg.src_addr[0])), 16))
	copy(syncCfg.DstEther[:], unsafe.Slice((*byte)(unsafe.Pointer(&cCfg.dst_ether)), 6))
	copy(syncCfg.DstAddrMulticast[:], unsafe.Slice((*byte)(unsafe.Pointer(&cCfg.dst_addr_multicast[0])), 16))
	copy(syncCfg.DstAddrUnicast[:], unsafe.Slice((*byte)(unsafe.Pointer(&cCfg.dst_addr_unicast[0])), 16))
	syncCfg.PortMulticast = uint16(ntohs(uint16(cCfg.port_multicast)))
	syncCfg.PortUnicast = uint16(ntohs(uint16(cCfg.port_unicast)))
	syncCfg.TcpSynAck = uint64(cCfg.timeouts.tcp_syn_ack)
	syncCfg.TcpSyn = uint64(cCfg.timeouts.tcp_syn)
	syncCfg.TcpFin = uint64(cCfg.timeouts.tcp_fin)
	syncCfg.Tcp = uint64(cCfg.timeouts.tcp)
	syncCfg.Udp = uint64(cCfg.timeouts.udp)
	syncCfg.Default = uint64(cCfg.timeouts.default_)
	return syncCfg
}

func (m SyncConfig) toC() C.struct_fwstate_sync_config {
	var cSyncConfig C.struct_fwstate_sync_config
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cSyncConfig.src_addr[0])), 16), m.SrcAddr[:])
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cSyncConfig.dst_ether)), 6), m.DstEther[:])
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cSyncConfig.dst_addr_multicast[0])), 16), m.DstAddrMulticast[:])
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cSyncConfig.dst_addr_unicast[0])), 16), m.DstAddrUnicast[:])
	cSyncConfig.port_multicast = C.uint16_t(htons(uint16(m.PortMulticast)))
	cSyncConfig.port_unicast = C.uint16_t(htons(uint16(m.PortUnicast)))
	cSyncConfig.timeouts.tcp_syn_ack = C.uint64_t(m.TcpSynAck)
	cSyncConfig.timeouts.tcp_syn = C.uint64_t(m.TcpSyn)
	cSyncConfig.timeouts.tcp_fin = C.uint64_t(m.TcpFin)
	cSyncConfig.timeouts.tcp = C.uint64_t(m.Tcp)
	cSyncConfig.timeouts.udp = C.uint64_t(m.Udp)
	cSyncConfig.timeouts.default_ = C.uint64_t(m.Default)

	return cSyncConfig
}

// NewCSyncConfig allocates a heap C struct fwstate_sync_config populated
// from sync.
//
// The caller owns the returned pointer and must release it with
// [FreeCSyncConfig] once the C side no longer references it. Returns nil
// on allocation failure.
func NewCSyncConfig(sync SyncConfig) unsafe.Pointer {
	cSyncConfig := sync.toC()
	ptr := C.calloc(1, C.sizeof_struct_fwstate_sync_config)
	if ptr == nil {
		return nil
	}
	*(*C.struct_fwstate_sync_config)(ptr) = cSyncConfig
	return ptr
}

// FreeCSyncConfig releases a pointer previously returned by
// [NewCSyncConfig]. Safe to call with nil.
func FreeCSyncConfig(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}
	C.free(ptr)
}

// StateKey stores a cursor key with address bytes as plain Go data.
type StateKey struct {
	Proto   uint32
	SrcPort uint32
	DstPort uint32
	SrcAddr []byte
	DstAddr []byte
}

// StateValue stores cursor value details for a state entry.
type StateValue struct {
	External        bool
	Flags           uint32
	CreatedAt       uint64
	UpdatedAt       uint64
	PacketsBackward uint64
	PacketsForward  uint64
}

// MapStats stores per-map statistics reported by fwstate.
type MapStats struct {
	IndexSize        uint32
	ExtraBucketCount uint32
	MaxChainLength   uint32
	LayerCount       uint32
	TotalElements    uint64
	MaxDeadline      uint64
	MemoryUsed       uint64
}

// MapsStats stores IPv4 and IPv6 fwmap statistics.
type MapsStats struct {
	IPv4 MapStats
	IPv6 MapStats
}

// CursorEntry represents a single entry read from the cursor.
type CursorEntry struct {
	Key     StateKey
	Value   StateValue
	Idx     uint32
	Expired bool
}

// SetModuleConfig configures a fwstate sync config in one call: links the
// named v4/v6 fwstate-map objects and copies the sync parameters.
//
// fw4Name and fw6Name are the object names of standalone fwstate_map_v4 /
// fwstate_map_v6 objects. Either may be empty, in which case no link is
// declared and the dataplane resolves a NULL fwtable for that family.
// Returns an error only on C-side link failure.
func SetModuleConfig(cp ffi.ModuleConfig, fw4Name, fw6Name string, sync SyncConfig) error {
	cSyncPtr := NewCSyncConfig(sync)
	if cSyncPtr == nil {
		return fmt.Errorf("failed to allocate fwstate sync config")
	}
	defer FreeCSyncConfig(cSyncPtr)

	var fw4CStr *C.char
	if fw4Name != "" {
		fw4CStr = C.CString(fw4Name)
		defer C.free(unsafe.Pointer(fw4CStr))
	}

	var fw6CStr *C.char
	if fw6Name != "" {
		fw6CStr = C.CString(fw6Name)
		defer C.free(unsafe.Pointer(fw6CStr))
	}

	var cErr *C.yanet_error
	rc := C.fwstate_module_config_set(
		(*C.struct_cp_module)(cp.AsRawPtr()),
		fw4CStr,
		fw6CStr,
		(*C.struct_fwstate_sync_config)(cSyncPtr),
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf("failed to set fwstate module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return nil
}

// GetSyncConfig retrieves the sync configuration from fwstate module.
func (m *ModuleConfig) GetSyncConfig() SyncConfig {
	cSyncConfig := C.fwstate_config_get_sync_config(m.asRawPtr())
	return newSyncConfigFromC(&cSyncConfig)
}

// fwmapStatsOrZero returns stats for the given head fwmap, or zero stats
// when head is nil (no map attached).
func fwmapStatsOrZero(head *C.fwmap_t) C.struct_fwmap_stats {
	if head == nil {
		return C.struct_fwmap_stats{}
	}
	return C.fwmap_get_stats(head)
}

func mapStatsFromC(stats C.struct_fwmap_stats) MapStats {
	return MapStats{
		IndexSize:        uint32(stats.index_size),
		ExtraBucketCount: uint32(stats.extra_bucket_count),
		MaxChainLength:   uint32(stats.max_chain_length),
		LayerCount:       uint32(stats.layer_count),
		TotalElements:    uint64(stats.total_elements),
		MaxDeadline:      uint64(stats.max_deadline),
		MemoryUsed:       uint64(stats.memory_used),
	}
}

func convertCKey(ptr unsafe.Pointer, isIPv6 bool) StateKey {
	var srcAddr []byte
	var dstAddr []byte
	if isIPv6 {
		k := (*C.struct_fw6_state_key)(ptr)
		srcAddr = C.GoBytes(unsafe.Pointer(&k.src_addr[0]), 16)
		dstAddr = C.GoBytes(unsafe.Pointer(&k.dst_addr[0]), 16)
	} else {
		k := (*C.struct_fw4_state_key)(ptr)
		srcAddr = make([]byte, 4)
		dstAddr = make([]byte, 4)
		*(*uint32)(unsafe.Pointer(&srcAddr[0])) = uint32(k.src_addr)
		*(*uint32)(unsafe.Pointer(&dstAddr[0])) = uint32(k.dst_addr)
	}

	hdr := (*C.struct_fw_state_key_hdr)(ptr)
	return StateKey{
		Proto:   uint32(hdr.proto),
		SrcPort: uint32(hdr.src_port),
		DstPort: uint32(hdr.dst_port),
		SrcAddr: srcAddr,
		DstAddr: dstAddr,
	}
}

func stateValueFromC(value *C.struct_fw_state_value) StateValue {
	return StateValue{
		External:        bool(value.external),
		Flags:           uint32(value.flags[0]),
		CreatedAt:       uint64(value.created_at),
		UpdatedAt:       uint64(value.updated_at),
		PacketsBackward: uint64(value.packets_backward),
		PacketsForward:  uint64(value.packets_forward),
	}
}

func htons(v uint16) uint16 {
	var beu16 [2]byte
	binary.BigEndian.PutUint16(beu16[:], v)
	return uint16(beu16[1])<<8 | uint16(beu16[0])
}

func ntohs(v uint16) uint16 {
	var beu16 [2]byte
	beu16[0] = uint8(v)
	beu16[1] = uint8(v >> 8)
	return binary.BigEndian.Uint16(beu16[:])
}
