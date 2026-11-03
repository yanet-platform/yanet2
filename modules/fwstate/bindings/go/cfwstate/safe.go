package cfwstate

//#include "lib/fwstate/types.h"
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
// port in host byte order; NewModuleConfig rejects a zero one.
type SyncEndpoint struct {
	SrcAddr [16]byte
	DstAddr [16]byte
	Port    uint16
}

// SyncListenEndpoint is one receive destination: an incoming packet naming
// it by IPv6 address and UDP port is claimed as external sync. The port is
// host byte order; NewModuleConfig rejects a zero one.
type SyncListenEndpoint struct {
	DstAddr [16]byte
	Port    uint16
}

// SyncConfig defines how fwstate emits locally generated synchronization
// events and which incoming packets it claims as external sync. Emission
// endpoints and listeners are independent lists, dynamically sized with
// no fixed bound.
type SyncConfig struct {
	Endpoints           []SyncEndpoint
	Listeners           []SyncListenEndpoint
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

func newSyncConfigFromC(cCfg *C.struct_fwstate_sync_config) SyncConfig {
	var syncCfg SyncConfig
	endpoints := unsafe.Slice((*C.struct_fwstate_sync_endpoint)(unsafe.Add(unsafe.Pointer(cCfg), C.FWSTATE_SYNC_ENDPOINTS_OFF)), int(cCfg.endpoint_count))
	for idx := range endpoints {
		cEndpoint := &endpoints[idx]
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
	listeners := unsafe.Slice((*C.struct_fwstate_sync_listen_endpoint)(unsafe.Add(unsafe.Pointer(cCfg), C.FWSTATE_SYNC_LISTENERS_OFF)), int(cCfg.listener_count))
	for idx := range listeners {
		cListener := &listeners[idx]
		var listener SyncListenEndpoint
		copy(listener.DstAddr[:], unsafe.Slice((*byte)(unsafe.Pointer(&cListener.dst_addr[0])), 16))
		listener.Port = uint16(ntohs(uint16(cListener.port)))
		syncCfg.Listeners = append(syncCfg.Listeners, listener)
	}
	syncCfg.Udp = uint64(cCfg.timeouts.udp)
	syncCfg.Default = uint64(cCfg.timeouts.default_)
	syncCfg.SyncSuppressTimeout = uint64(cCfg.sync_suppress_timeout)
	syncCfg.SyncMTU = uint16(cCfg.sync_mtu)
	return syncCfg
}

func (m SyncConfig) toC() C.struct_fwstate_sync_config {
	var cSyncConfig C.struct_fwstate_sync_config
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

// endpointsToC builds the flat endpoint array the C entry copies into
// the config's memory tree.
func (m SyncConfig) endpointsToC() []C.struct_fwstate_sync_endpoint {
	endpoints := make([]C.struct_fwstate_sync_endpoint, len(m.Endpoints))
	for idx, endpoint := range m.Endpoints {
		cEndpoint := &endpoints[idx]
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&cEndpoint.src_addr[0])), 16), endpoint.SrcAddr[:])
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&cEndpoint.dst_addr[0])), 16), endpoint.DstAddr[:])
		cEndpoint.port = C.uint16_t(htons(uint16(endpoint.Port)))
	}
	return endpoints
}

// listenersToC builds the flat listener array the C entry copies into
// the config's memory tree.
func (m SyncConfig) listenersToC() []C.struct_fwstate_sync_listen_endpoint {
	listeners := make([]C.struct_fwstate_sync_listen_endpoint, len(m.Listeners))
	for idx, listener := range m.Listeners {
		cListener := &listeners[idx]
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&cListener.dst_addr[0])), 16), listener.DstAddr[:])
		cListener.port = C.uint16_t(htons(uint16(listener.Port)))
	}
	return listeners
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
