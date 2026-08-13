package cfwstate

//#include "lib/fwstate/config.h"
//#include "lib/fwstate/fwstate_cursor.h"
//#include "modules/fwstate/api/fwstate_cp.h"
import "C"

import (
	"encoding/binary"
	"unsafe"
)

// TTL48Max is the largest TTL (ns) storable in fw_state_value::last_ttl.
const TTL48Max = uint64(C.FWSTATE_TTL48_MAX)

// SyncConfig stores the receive-side fwstate synchronization settings
// the fwstate module consumes: packet matching, timeouts, suppression.
type SyncConfig struct {
	SrcAddr             [16]byte
	DstAddrMulticast    [16]byte
	PortMulticast       uint16
	TcpSynAck           uint64
	TcpSyn              uint64
	TcpFin              uint64
	Tcp                 uint64
	Udp                 uint64
	Default             uint64
	SyncSuppressTimeout uint64
}

// SyncEmitConfig stores the emission-side sync addressing a module
// synthesizing state-sync packets (ACL CREATE_STATE) needs.
type SyncEmitConfig struct {
	DstEther         [6]byte
	DstAddrMulticast [16]byte
	PortMulticast    uint16
	DstAddrUnicast   [16]byte
	PortUnicast      uint16
}

func newSyncConfigFromC(cCfg *C.struct_fwstate_sync_config) SyncConfig {
	var syncCfg SyncConfig
	copy(syncCfg.SrcAddr[:], unsafe.Slice((*byte)(unsafe.Pointer(&cCfg.src_addr[0])), 16))
	copy(syncCfg.DstAddrMulticast[:], unsafe.Slice((*byte)(unsafe.Pointer(&cCfg.dst_addr_multicast[0])), 16))
	syncCfg.PortMulticast = uint16(ntohs(uint16(cCfg.port_multicast)))
	syncCfg.TcpSynAck = uint64(cCfg.timeouts.tcp_syn_ack)
	syncCfg.TcpSyn = uint64(cCfg.timeouts.tcp_syn)
	syncCfg.TcpFin = uint64(cCfg.timeouts.tcp_fin)
	syncCfg.Tcp = uint64(cCfg.timeouts.tcp)
	syncCfg.Udp = uint64(cCfg.timeouts.udp)
	syncCfg.Default = uint64(cCfg.timeouts.default_)
	syncCfg.SyncSuppressTimeout = uint64(cCfg.sync_suppress_timeout)
	return syncCfg
}

func (m SyncConfig) toC() C.struct_fwstate_sync_config {
	var cSyncConfig C.struct_fwstate_sync_config
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cSyncConfig.src_addr[0])), 16), m.SrcAddr[:])
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cSyncConfig.dst_addr_multicast[0])), 16), m.DstAddrMulticast[:])
	cSyncConfig.port_multicast = C.uint16_t(htons(uint16(m.PortMulticast)))
	cSyncConfig.timeouts.tcp_syn_ack = C.uint64_t(m.TcpSynAck)
	cSyncConfig.timeouts.tcp_syn = C.uint64_t(m.TcpSyn)
	cSyncConfig.timeouts.tcp_fin = C.uint64_t(m.TcpFin)
	cSyncConfig.timeouts.tcp = C.uint64_t(m.Tcp)
	cSyncConfig.timeouts.udp = C.uint64_t(m.Udp)
	cSyncConfig.timeouts.default_ = C.uint64_t(m.Default)
	cSyncConfig.sync_suppress_timeout = C.uint64_t(m.SyncSuppressTimeout)

	return cSyncConfig
}

func (m SyncEmitConfig) toC() C.struct_fwstate_sync_emit_config {
	var cEmitConfig C.struct_fwstate_sync_emit_config
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cEmitConfig.dst_ether)), 6), m.DstEther[:])
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cEmitConfig.dst_addr_multicast[0])), 16), m.DstAddrMulticast[:])
	cEmitConfig.port_multicast = C.uint16_t(htons(uint16(m.PortMulticast)))
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cEmitConfig.dst_addr_unicast[0])), 16), m.DstAddrUnicast[:])
	cEmitConfig.port_unicast = C.uint16_t(htons(uint16(m.PortUnicast)))

	return cEmitConfig
}

// NewCEmitSyncConfig allocates a C-side copy of the emission sync
// configuration that other modules' bindings can pass to their C APIs.
//
// The caller owns the returned pointer and must release it with
// [FreeCEmitSyncConfig] once the C side no longer references it. Returns
// nil on allocation failure.
func NewCEmitSyncConfig(emit SyncEmitConfig) unsafe.Pointer {
	cEmitConfig := emit.toC()
	ptr := C.calloc(1, C.sizeof_struct_fwstate_sync_emit_config)
	if ptr == nil {
		return nil
	}
	*(*C.struct_fwstate_sync_emit_config)(ptr) = cEmitConfig
	return ptr
}

// FreeCEmitSyncConfig releases a pointer previously returned by
// [NewCEmitSyncConfig]. Safe to call with nil.
func FreeCEmitSyncConfig(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}
	C.free(ptr)
}

// GetSyncConfig retrieves the sync configuration from fwstate module.
func (m *ModuleConfig) GetSyncConfig() SyncConfig {
	cSyncConfig := C.fwstate_config_get_sync_config(m.asRawPtr())
	return newSyncConfigFromC(&cSyncConfig)
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
