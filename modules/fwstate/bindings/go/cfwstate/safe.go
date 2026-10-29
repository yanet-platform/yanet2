package cfwstate

//#include "lib/fwstate/config.h"
//#include "lib/fwstate/fwstate_cursor.h"
//#include "lib/fwstate/sync.h"
//#include "modules/fwstate/api/fwstate_cp.h"
import "C"

import (
	"encoding/binary"
	"unsafe"
)

// TTL48Max is the largest TTL (ns) storable in fw_state_value::last_ttl.
const TTL48Max = uint64(C.FWSTATE_TTL48_MAX)

// SyncEndpoint is one synchronization destination: the source stamped on
// packets emitted toward it, the destination address, and the destination
// port in host byte order. A zero Port disables the endpoint.
type SyncEndpoint struct {
	SrcAddr [16]byte
	DstAddr [16]byte
	Port    uint16
}

// SyncConfig defines how fwstate receives synchronization packets and emits
// locally generated synchronization events. Every endpoint is used for
// emission; an endpoint with a multicast destination is matched on the wire
// for reception as well.
type SyncConfig struct {
	// Endpoints carries at most MaxSyncEndpoints entries.
	Endpoints           []SyncEndpoint
	TcpSynAck           uint64
	TcpSyn              uint64
	TcpFin              uint64
	Tcp                 uint64
	Udp                 uint64
	Default             uint64
	SyncSuppressTimeout uint64
	// SyncMTU bounds the size of one emitted sync packet, IPv6 and UDP
	// headers included; zero selects the default.
	SyncMTU uint16
}

// MinSyncMTU is the smallest sync MTU that carries one frame.
const MinSyncMTU = uint16(C.FWSTATE_SYNC_MIN_MTU)

// MaxSyncEndpoints mirrors the C FWSTATE_SYNC_ENDPOINTS_MAX bound of the
// endpoint array in the shared-memory config.
const MaxSyncEndpoints = int(C.FWSTATE_SYNC_ENDPOINTS_MAX)

func newSyncConfigFromC(cCfg *C.struct_fwstate_sync_config) SyncConfig {
	var syncCfg SyncConfig
	count := min(int(cCfg.endpoint_count), MaxSyncEndpoints)
	for idx := range count {
		cEndpoint := &cCfg.endpoints[idx]
		var endpoint SyncEndpoint
		copy(endpoint.SrcAddr[:], unsafe.Slice((*byte)(unsafe.Pointer(&cEndpoint.src_addr[0])), 16))
		copy(endpoint.DstAddr[:], unsafe.Slice((*byte)(unsafe.Pointer(&cEndpoint.dst_addr[0])), 16))
		endpoint.Port = uint16(ntohs(uint16(cEndpoint.port)))
		syncCfg.Endpoints = append(syncCfg.Endpoints, endpoint)
	}
	syncCfg.TcpSynAck = uint64(cCfg.timeouts.tcp_syn_ack)
	syncCfg.TcpSyn = uint64(cCfg.timeouts.tcp_syn)
	syncCfg.TcpFin = uint64(cCfg.timeouts.tcp_fin)
	syncCfg.Tcp = uint64(cCfg.timeouts.tcp)
	syncCfg.Udp = uint64(cCfg.timeouts.udp)
	syncCfg.Default = uint64(cCfg.timeouts.default_)
	syncCfg.SyncSuppressTimeout = uint64(cCfg.sync_suppress_timeout)
	syncCfg.SyncMTU = uint16(cCfg.sync_mtu)
	return syncCfg
}

func (m SyncConfig) toC() C.struct_fwstate_sync_config {
	var cSyncConfig C.struct_fwstate_sync_config
	count := min(len(m.Endpoints), MaxSyncEndpoints)
	cSyncConfig.endpoint_count = C.uint32_t(count)
	for idx := range count {
		endpoint := &m.Endpoints[idx]
		cEndpoint := &cSyncConfig.endpoints[idx]
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&cEndpoint.src_addr[0])), 16), endpoint.SrcAddr[:])
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&cEndpoint.dst_addr[0])), 16), endpoint.DstAddr[:])
		cEndpoint.port = C.uint16_t(htons(uint16(endpoint.Port)))
	}
	cSyncConfig.timeouts.tcp_syn_ack = C.uint64_t(m.TcpSynAck)
	cSyncConfig.timeouts.tcp_syn = C.uint64_t(m.TcpSyn)
	cSyncConfig.timeouts.tcp_fin = C.uint64_t(m.TcpFin)
	cSyncConfig.timeouts.tcp = C.uint64_t(m.Tcp)
	cSyncConfig.timeouts.udp = C.uint64_t(m.Udp)
	cSyncConfig.timeouts.default_ = C.uint64_t(m.Default)
	cSyncConfig.sync_suppress_timeout = C.uint64_t(m.SyncSuppressTimeout)
	cSyncConfig.sync_mtu = C.uint16_t(m.SyncMTU)

	return cSyncConfig
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
