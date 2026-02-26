package fwstate

/*
#cgo CFLAGS: -I../../../ -I../../../lib
#cgo LDFLAGS: -L../../../build/modules/fwstate/api -lfwstate_cp

#include "api/agent.h"
#include "common/numutils.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/fwstate_cursor.h"
#include "modules/fwstate/api/fwstate_cp.h"
#include "modules/fwstate/dataplane/config.h"
*/
import "C"

import (
	"fmt"
	"unsafe"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb"
)

// FwStateConfig wraps the C fwstate configuration
type FwStateConfig struct {
	name       string
	ptr        ffi.ModuleConfig
	generation uint64
}

// NewFWStateModuleConfig creates a new FWState module configuration
func NewFWStateModuleConfig(agent *ffi.Agent, name string) (*FwStateConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.fwstate_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName)
	if ptr == nil {
		if err != nil {
			return nil, fmt.Errorf("failed to initialize FWState module config: %w", err)
		}
		return nil, fmt.Errorf("failed to initialize FWState module config: module '%s' not found", name)
	}

	return &FwStateConfig{
		name: name,
		ptr:  ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *FwStateConfig) Name() string {
	return m.name
}

func (m *FwStateConfig) PropogateConfig(old *FwStateConfig) {
	C.fwstate_module_config_propogate(m.asCPModule(), old.asCPModule())
}

func (m *FwStateConfig) asCPModule() *C.struct_cp_module {
	if m == nil {
		return nil
	}
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

func (m *FwStateConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

func (m *FwStateConfig) Generation() uint64 {
	return m.generation
}

// CreateMaps creates firewall state maps
func (m *FwStateConfig) CreateMaps(
	mapConfig *fwstatepb.MapConfig,
	workerCount uint16,
	log *zap.SugaredLogger,
) error {
	mapConfigChanged := false
	mapsStats := m.GetMapsStats()
	// TODO: support separate map size for v4 and v6
	currentIndexSize := uint32(max(mapsStats.v4.index_size, mapsStats.v6.index_size))
	currentExtraBucketCount := uint32(max(mapsStats.v4.extra_bucket_count, mapsStats.v6.extra_bucket_count))
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

		log.Infow("inserting new fwstate layer",
			zap.Uint32("index_size", currentIndexSize),
			zap.Uint32("extra_bucket_count", currentExtraBucketCount),
			zap.Uint16("worker_count", workerCount),
		)

		if rc, cErr := C.fwstate_config_insert_new_layer(
			m.asCPModule(),
			C.uint32_t(currentIndexSize),
			C.uint32_t(currentExtraBucketCount),
			C.uint16_t(workerCount),
		); rc != 0 {
			return fmt.Errorf("failed to insert new layer: error code=%d, cErr=%v", rc, cErr)
		}

		m.generation++
		return nil
	}

	log.Infow("creating fwstate maps",
		zap.Uint32("index_size", mapConfig.IndexSize),
		zap.Uint32("extra_bucket_count", mapConfig.ExtraBucketCount),
		zap.Uint16("worker_count", workerCount),
	)

	if rc, cErr := C.fwstate_config_create_maps(
		m.asCPModule(),
		C.uint32_t(currentIndexSize),
		C.uint32_t(currentExtraBucketCount),
		C.uint16_t(workerCount),
	); rc != 0 {
		return fmt.Errorf("failed to create maps: error code=%d, cErr=%v", rc, cErr)
	}

	m.generation++
	return nil
}

// SetSyncConfig sets the synchronization configuration
func (m *FwStateConfig) SetSyncConfig(req *fwstatepb.SyncConfig) {
	currentConfig := m.GetSyncConfig()

	// Check byte arrays (addresses)
	if len(req.SrcAddr) == 0 {
		req.SrcAddr = make([]byte, 16)
		copy(req.SrcAddr, currentConfig.SrcAddr)
	}

	if len(req.DstEther) == 0 {
		req.DstEther = make([]byte, 6)
		copy(req.DstEther, currentConfig.DstEther)
	}

	if len(req.DstAddrMulticast) == 0 {
		req.DstAddrMulticast = make([]byte, 16)
		copy(req.DstAddrMulticast, currentConfig.DstAddrMulticast)
	}

	if len(req.DstAddrUnicast) == 0 {
		req.DstAddrUnicast = make([]byte, 16)
		copy(req.DstAddrUnicast, currentConfig.DstAddrUnicast)
	}

	// Check ports
	if req.PortMulticast == 0 {
		req.PortMulticast = currentConfig.PortMulticast
	}

	if req.PortUnicast == 0 {
		req.PortUnicast = currentConfig.PortUnicast
	}

	// Check timeouts
	if req.TcpSynAck == 0 {
		req.TcpSynAck = currentConfig.TcpSynAck
	}

	if req.TcpSyn == 0 {
		req.TcpSyn = currentConfig.TcpSyn
	}

	if req.TcpFin == 0 {
		req.TcpFin = currentConfig.TcpFin
	}

	if req.Tcp == 0 {
		req.Tcp = currentConfig.Tcp
	}

	if req.Udp == 0 {
		req.Udp = currentConfig.Udp
	}

	if req.Default == 0 {
		req.Default = currentConfig.Default
	}

	cSyncConfig := ConvertPbToCSyncConfig(req)
	C.fwstate_module_config_set_sync_config(m.asCPModule(), &cSyncConfig)
}

type mapsStats struct {
	v4 C.struct_fwmap_stats
	v6 C.struct_fwmap_stats
}

func (m *FwStateConfig) GetMapsStats() mapsStats {
	return mapsStats{
		v4: C.fwstate_config_get_map_stats(m.asCPModule(), C.bool(false /* v4 */)),
		v6: C.fwstate_config_get_map_stats(m.asCPModule(), C.bool(true /* v6 */)),
	}
}

// GetSyncConfig retrieves the sync configuration from fwstate module
func (m *FwStateConfig) GetSyncConfig() *fwstatepb.SyncConfig {
	if m == nil || m.ptr.AsRawPtr() == nil {
		return nil
	}
	cSyncConfig := C.fwstate_config_get_sync_config(m.asCPModule())
	return ConvertCSyncConfigToPb(&cSyncConfig)
}

// GetMapConfig retrieves the map configuration from fwstate module
func (m *FwStateConfig) GetMapConfig() *fwstatepb.MapConfig {
	if m == nil || m.ptr.AsRawPtr() == nil {
		return nil
	}
	stats := m.GetMapsStats()
	// Use v4 stats as reference (both v4 and v6 should have same config)
	indexSize := uint32(stats.v4.index_size)
	extraBucketCount := uint32(stats.v4.extra_bucket_count)
	// If v4 is empty, try v6
	if indexSize == 0 {
		indexSize = uint32(stats.v6.index_size)
		extraBucketCount = uint32(stats.v6.extra_bucket_count)
	}
	return &fwstatepb.MapConfig{
		IndexSize:        indexSize,
		ExtraBucketCount: extraBucketCount,
	}
}

func (m *FwStateConfig) DetachMaps() {
	C.fwstate_module_config_detach_maps(m.asCPModule())
}

// Free frees the fwstate configuration
func (m *FwStateConfig) Free() {
	C.fwstate_module_config_free(m.asCPModule())
}

// CursorEntry represents a single entry read from the cursor.
// Key and value bytes are copied from C memory and safe to hold after unlock.
type CursorEntry struct {
	Key     *fwstatepb.FwStateKey
	Value   *fwstatepb.FwStateValue
	Idx     uint32
	Expired bool
}

// ReadForward reads up to count entries in the forward direction starting at index.
// The caller must hold FWStateService.mu.
func (m *FwStateConfig) ReadForward(
	isIPv6 bool, layerIndex uint32,
	index uint32, includeExpired bool,
	now uint64, count uint32,
) (entries []CursorEntry, newIndex uint32, hasMore bool, err error) {
	return m.readEntries(isIPv6, layerIndex, index, includeExpired, now, count, false)
}

// ReadBackward reads up to count entries in the backward direction starting at index.
// The caller must hold FWStateService.mu.
func (m *FwStateConfig) ReadBackward(
	isIPv6 bool, layerIndex uint32,
	index uint32, includeExpired bool,
	now uint64, count uint32,
) (entries []CursorEntry, newIndex uint32, hasMore bool, err error) {
	return m.readEntries(isIPv6, layerIndex, index, includeExpired, now, count, true)
}

func (m *FwStateConfig) readEntries(
	isIPv6 bool, layerIndex uint32,
	index uint32, includeExpired bool,
	now uint64, count uint32, backward bool,
) ([]CursorEntry, uint32, bool, error) {
	var cursor C.fwstate_cursor_t
	rc := C.fwstate_config_cursor_create(
		m.asCPModule(), &cursor,
		C.bool(isIPv6), C.uint32_t(layerIndex),
		C.uint32_t(index), C.bool(includeExpired),
	)
	if rc != 0 {
		return nil, 0, false, fmt.Errorf("failed to create cursor: map or layer not found")
	}

	fwmap := C.fwstate_config_resolve_map(
		m.asCPModule(), C.bool(isIPv6), C.uint32_t(layerIndex),
	)
	if fwmap == nil {
		return nil, 0, false, fmt.Errorf("failed to resolve map")
	}

	buf := make([]C.fwstate_cursor_entry_t, count)

	var n C.int32_t
	if backward {
		n = C.fwstate_cursor_read_backward(
			fwmap, &cursor, C.uint64_t(now), &buf[0], C.uint32_t(count),
		)
	} else {
		n = C.fwstate_cursor_read_forward(
			fwmap, &cursor, C.uint64_t(now), &buf[0], C.uint32_t(count),
		)
	}

	entries := make([]CursorEntry, 0, n)
	for i := range n {
		entry := buf[i]
		val := (*C.struct_fw_state_value)(entry.value)

		ttl := C.fwstate_entry_ttl(val, &cursor.timeouts)
		expired := val.updated_at+ttl <= C.uint64_t(now)

		pbKey := convertCKey(entry.key, isIPv6)
		pbVal := &fwstatepb.FwStateValue{
			External:             bool(val.external),
			ProtocolType:         uint32(val._type),
			Flags:                uint32(val.flags[0]),
			PacketsSinceLastSync: uint32(val.packets_since_last_sync),
			CreatedAt:            uint64(val.created_at),
			UpdatedAt:            uint64(val.updated_at),
			PacketsBackward:      uint64(val.packets_backward),
			PacketsForward:       uint64(val.packets_forward),
		}

		entries = append(entries, CursorEntry{
			Key:     pbKey,
			Value:   pbVal,
			Idx:     uint32(entry.idx),
			Expired: bool(expired),
		})
	}

	newIndex := uint32(cursor.key_pos)
	// Safe to read key_cursor directly - we hold mu.
	keyLimit := uint32(fwmap.key_cursor)

	hasMore := false
	if backward {
		if n > 0 {
			// We can't use newIndex > 0 because the entry with index 0 would be lost
			hasMore = uint32(buf[n-1].idx) > 0
		}
	} else {
		hasMore = newIndex < keyLimit
	}

	return entries, newIndex, hasMore, nil
}

func convertCKey(ptr unsafe.Pointer, isIPv6 bool) *fwstatepb.FwStateKey {
	if isIPv6 {
		k := (*C.struct_fw6_state_key)(ptr)
		srcAddr := C.GoBytes(unsafe.Pointer(&k.src_addr[0]), 16)
		dstAddr := C.GoBytes(unsafe.Pointer(&k.dst_addr[0]), 16)
		return &fwstatepb.FwStateKey{
			Proto:   uint32(k.proto),
			SrcPort: uint32(k.src_port),
			DstPort: uint32(k.dst_port),
			SrcAddr: &fwstatepb.Addr{Bytes: srcAddr},
			DstAddr: &fwstatepb.Addr{Bytes: dstAddr},
		}
	}

	k := (*C.struct_fw4_state_key)(ptr)
	srcAddr := make([]byte, 4)
	dstAddr := make([]byte, 4)
	*(*uint32)(unsafe.Pointer(&srcAddr[0])) = uint32(k.src_addr)
	*(*uint32)(unsafe.Pointer(&dstAddr[0])) = uint32(k.dst_addr)
	return &fwstatepb.FwStateKey{
		Proto:   uint32(k.proto),
		SrcPort: uint32(k.src_port),
		DstPort: uint32(k.dst_port),
		SrcAddr: &fwstatepb.Addr{Bytes: srcAddr},
		DstAddr: &fwstatepb.Addr{Bytes: dstAddr},
	}
}

// OutdatedLayers represents a handle to outdated layers that need to be freed
type OutdatedLayers struct {
	ptr unsafe.Pointer
}

// TrimStaleLayers trims stale layers from both IPv4 and IPv6 maps
// Returns handle to outdated layers that should be freed after UpdateModules
// Returns nil on error
func (m *FwStateConfig) TrimStaleLayers(now uint64) *OutdatedLayers {
	ptr, err := C.fwstate_config_trim_stale_layers(m.asCPModule(), C.uint64_t(now))
	if ptr == nil {
		if err != nil {
			return nil
		}
		return nil
	}
	m.generation++
	return &OutdatedLayers{ptr: unsafe.Pointer(ptr)}
}

// FreeOutdatedLayers frees outdated layers after successful UpdateModules
func (m *FwStateConfig) FreeOutdatedLayers(outdated *OutdatedLayers) {
	if outdated == nil || outdated.ptr == nil {
		return
	}
	C.fwstate_outdated_layers_free(
		(*C.fwstate_outdated_layers_t)(outdated.ptr),
		m.asCPModule(),
	)
	outdated.ptr = nil
}
