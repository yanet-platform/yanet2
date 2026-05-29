package cfwstate

//#include "lib/fwstate/config.h"
//#include "lib/fwstate/fwstate_cursor.h"
//#include "modules/fwstate/api/fwstate_cp.h"
//#include "common/numutils.h"
import "C"

import (
	"encoding/binary"
	"fmt"
	"unsafe"
)

type MapConfig struct {
	IndexSize        uint32
	ExtraBucketCount uint32
}

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

// OutdatedLayers represents a handle to outdated layers that need to be freed.
type OutdatedLayers struct {
	ptr unsafe.Pointer
}

// CreateMaps creates firewall state maps.
func (m *ModuleConfig) CreateMaps(
	mapConfig MapConfig,
	workerCount uint16,
) error {
	mapConfigChanged := false
	mapsStats := m.GetMapsStats()
	currentIndexSize := max(mapsStats.IPv4.IndexSize, mapsStats.IPv6.IndexSize)
	currentExtraBucketCount := max(mapsStats.IPv4.ExtraBucketCount, mapsStats.IPv6.ExtraBucketCount)
	mapsExist := currentIndexSize != 0
	requestedIndexSize := uint32(C.align_up_pow2(C.uint64_t(mapConfig.IndexSize)))
	requestedExtraBucketCount := uint32(C.align_up_pow2(C.uint64_t(mapConfig.ExtraBucketCount)))

	if requestedIndexSize != 0 && requestedIndexSize != currentIndexSize {
		mapConfigChanged = true
		currentIndexSize = mapConfig.IndexSize
	}
	if requestedExtraBucketCount != 0 && requestedExtraBucketCount != currentExtraBucketCount {
		mapConfigChanged = true
		currentExtraBucketCount = mapConfig.ExtraBucketCount
	}
	if mapsExist {
		if !mapConfigChanged {
			return nil
		}

		if rc, cErr := C.fwstate_config_insert_new_layer(
			m.asRawPtr(),
			C.uint32_t(currentIndexSize),
			C.uint32_t(currentExtraBucketCount),
			C.uint16_t(workerCount),
		); rc != 0 {
			return fmt.Errorf("failed to insert new layer: error code=%d, cErr=%v", rc, cErr)
		}

		m.generation++
		return nil
	}

	if rc, cErr := C.fwstate_config_create_maps(
		m.asRawPtr(),
		C.uint32_t(currentIndexSize),
		C.uint32_t(currentExtraBucketCount),
		C.uint16_t(workerCount),
	); rc != 0 {
		return fmt.Errorf("failed to create maps: error code=%d, cErr=%v", rc, cErr)
	}

	m.generation++
	return nil
}

// SetSyncConfig sets the synchronization configuration.
func (m *ModuleConfig) SetSyncConfig(req SyncConfig) {
	cSyncConfig := req.toC()
	C.fwstate_module_config_set_sync_config(m.asRawPtr(), &cSyncConfig)
}

// GetMapsStats retrieves IPv4 and IPv6 map stats.
func (m *ModuleConfig) GetMapsStats() MapsStats {
	return MapsStats{
		IPv4: mapStatsFromC(C.fwstate_config_get_map_stats(m.asRawPtr(), C.bool(false))),
		IPv6: mapStatsFromC(C.fwstate_config_get_map_stats(m.asRawPtr(), C.bool(true))),
	}
}

// GetSyncConfig retrieves the sync configuration from fwstate module.
func (m *ModuleConfig) GetSyncConfig() SyncConfig {
	cSyncConfig := C.fwstate_config_get_sync_config(m.asRawPtr())
	return newSyncConfigFromC(&cSyncConfig)
}

// GetMapConfig retrieves the map configuration from fwstate module.
func (m *ModuleConfig) GetMapConfig() MapConfig {
	stats := m.GetMapsStats()
	indexSize := stats.IPv4.IndexSize
	extraBucketCount := stats.IPv4.ExtraBucketCount
	if indexSize == 0 {
		indexSize = stats.IPv6.IndexSize
		extraBucketCount = stats.IPv6.ExtraBucketCount
	}

	return MapConfig{
		IndexSize:        indexSize,
		ExtraBucketCount: extraBucketCount,
	}
}

// FreeOutdatedLayers frees outdated layers after successful UpdateModules.
func (m *ModuleConfig) FreeOutdatedLayers(outdated *OutdatedLayers) {
	if outdated == nil || outdated.ptr == nil {
		return
	}
	C.fwstate_outdated_layers_free(
		(*C.fwstate_outdated_layers_t)(outdated.ptr),
		m.asRawPtr(),
	)
	outdated.ptr = nil
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

// ReadForward reads up to count entries in the forward direction.
func (m *ModuleConfig) ReadForward(
	isIPv6 bool,
	layerIndex uint32,
	index int64,
	includeExpired bool,
	now uint64,
	count uint32,
) ([]CursorEntry, int64, bool, error) {
	return m.readEntries(isIPv6, layerIndex, index, includeExpired, now, count, false)
}

// ReadBackward reads up to count entries in the backward direction.
func (m *ModuleConfig) ReadBackward(
	isIPv6 bool,
	layerIndex uint32,
	index int64,
	includeExpired bool,
	now uint64,
	count uint32,
) ([]CursorEntry, int64, bool, error) {
	return m.readEntries(isIPv6, layerIndex, index, includeExpired, now, count, true)
}

func (m *ModuleConfig) readEntries(
	isIPv6 bool,
	layerIndex uint32,
	index int64,
	includeExpired bool,
	now uint64,
	count uint32,
	backward bool,
) ([]CursorEntry, int64, bool, error) {
	var cursor C.fwstate_cursor_t
	rc := C.fwstate_config_cursor_create(
		m.asRawPtr(), &cursor,
		C.bool(isIPv6), C.uint32_t(layerIndex),
		C.int64_t(index), C.bool(includeExpired),
	)
	if rc != 0 {
		return nil, 0, false, fmt.Errorf("failed to create cursor: map or layer not found")
	}

	fwmap := C.fwstate_config_resolve_map(
		m.asRawPtr(), C.bool(isIPv6), C.uint32_t(layerIndex),
	)
	if fwmap == nil {
		return nil, 0, false, fmt.Errorf("failed to resolve map")
	}

	if count == 0 {
		return nil, int64(cursor.key_pos), false, nil
	}

	buf := make([]C.fwstate_cursor_entry_t, count)
	var cEntries *C.fwstate_cursor_entry_t
	if len(buf) > 0 {
		cEntries = &buf[0]
	}

	var n C.uint32_t
	if backward {
		n = C.fwstate_cursor_read_backward(fwmap, &cursor, C.uint64_t(now), cEntries, C.uint32_t(count))
	} else {
		n = C.fwstate_cursor_read_forward(fwmap, &cursor, C.uint64_t(now), cEntries, C.uint32_t(count))
	}

	entries := make([]CursorEntry, 0, n)
	for idx := range n {
		entry := buf[idx]
		val := (*C.struct_fw_state_value)(entry.value)

		stateKey := convertCKey(entry.key, isIPv6)
		stateValue := stateValueFromC(val)

		entries = append(entries, CursorEntry{
			Key:     stateKey,
			Value:   stateValue,
			Idx:     uint32(entry.idx),
			Expired: bool(entry.expired),
		})
	}

	newIndex := int64(cursor.key_pos)
	keyLimit := fwmap.key_cursor
	hasMore := false
	if backward {
		hasMore = newIndex > -1
	} else {
		hasMore = newIndex < int64(keyLimit)
	}

	return entries, newIndex, hasMore, nil
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

// TrimStaleLayers trims stale layers from both IPv4 and IPv6 maps.
func (m *ModuleConfig) TrimStaleLayers(now uint64) *OutdatedLayers {
	ptr := C.fwstate_config_trim_stale_layers(m.asRawPtr(), C.uint64_t(now))
	if ptr == nil {
		return nil
	}
	m.generation++
	return &OutdatedLayers{ptr: unsafe.Pointer(ptr)}
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
