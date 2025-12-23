package fwstate

/*
#cgo CFLAGS: -I../../../ -I../../../lib
#cgo LDFLAGS: -L../../../build/modules/fwstate/api -lfwstate_cp

#include "api/agent.h"
#include "common/numutils.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwmap.h"
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
	name string
	ptr  ffi.ModuleConfig
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

func (m *FwStateConfig) TransferConfig(old *FwStateConfig) {
	C.fwstate_module_config_transfer(m.asCPModule(), old.asCPModule())
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
		// TODO: implement layer rotation
		// Layer rotation is safe without additional synchronization primitives because:
		// - Config updates create a new generation via update_modules
		// - Rotation happens under dataplane lock
		// - All modules atomically see the new map links
		return fmt.Errorf("layer rotation not yet implemented")
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
