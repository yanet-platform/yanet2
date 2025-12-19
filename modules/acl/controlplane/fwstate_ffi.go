package acl

/*
#cgo CFLAGS: -I../../../
#cgo CFLAGS: -I../../../lib
#cgo LDFLAGS: -L../../../build/modules/acl/api -lacl_cp

#include "api/agent.h"
#include "modules/acl/api/fwstate_cp.h"
#include "modules/fwstate/dataplane/config.h"
#include "fwstate/config.h"
#include "fwstate/fwmap.h"

char* module_type_name() {
	return FWSTATE_MODULE_NAME;
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
	"go.uber.org/zap"
)

var fwstateModuleTypeName = C.GoString(C.module_type_name())

// FwStateConfig wraps the C fwstate configuration within ACL module
type FwStateConfig struct {
	ptr ffi.ModuleConfig
}

// NewFWStateModuleConfig creates a new ACL module configuration
func NewFWStateModuleConfig(agent *ffi.Agent, name string, oldConfig *FwStateConfig) (*FwStateConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.fwstate_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName, oldConfig.asCPModule())
	if ptr == nil {
		if err != nil {
			return nil, fmt.Errorf("failed to initialize FWState module config: %w", err)
		}
		return nil, fmt.Errorf("failed to initialize FWState module config: module '%s' not found", name)
	}

	return &FwStateConfig{ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr))}, nil
}

func (m *FwStateConfig) asCPModule() *C.struct_cp_module {
	if m == nil {
		return nil
	}
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

func (m *FwStateConfig) asFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// CreateMaps creates firewall state maps
func (m *FwStateConfig) CreateMaps(
	mapConfig *aclpb.MapConfig,
	workerCount uint16,
	log *zap.SugaredLogger,
) error {
	mapConfigChanged := false
	mapsStats := m.GetMapsStats()
	// TODO: support separate map size for v4 and v6
	indexSize := max(mapsStats.v4.index_size, mapsStats.v6.index_size)
	mapsExist := indexSize != 0
	extraBucketCount := max(mapsStats.v4.extra_bucket_count, mapsStats.v6.extra_bucket_count)
	if mapConfig.IndexSize != 0 && mapConfig.IndexSize != uint32(indexSize) {
		mapConfigChanged = true
		indexSize = C.uint32_t(mapConfig.IndexSize)
	}
	if mapConfig.ExtraBucketCount != 0 && mapConfig.ExtraBucketCount != uint32(extraBucketCount) {
		mapConfigChanged = true
		extraBucketCount = C.uint32_t(mapConfig.ExtraBucketCount)
	}
	if mapsExist {
		if !mapConfigChanged {
			return nil
		}
		// FIXME rotate layers
		return fmt.Errorf("TODO: layers rotation is not connected to C code yet")
	}

	log.Infow("creating fwstate maps",
		zap.Uint32("index_size", mapConfig.IndexSize),
		zap.Uint32("extra_bucket_count", mapConfig.ExtraBucketCount),
		zap.Uint16("worker_count", workerCount),
	)

	if rc, cErr := C.fwstate_config_create_maps(
		m.asCPModule(),
		C.uint32_t(indexSize),
		C.uint32_t(extraBucketCount),
		C.uint16_t(workerCount),
	); rc != 0 {
		return fmt.Errorf("failed to create maps: error code=%d, cErr=%v", rc, cErr)
	}

	return nil
}

// SetSyncConfig sets the synchronization configuration
func (m *FwStateConfig) SetSyncConfig(newConfig *aclpb.SyncConfig) {
	oldConfig := m.GetSyncConfig()

	// If oldConfig is nil, we can't copy values from it
	// Copy zero-value fields from oldConfig to newConfig

	// Check byte arrays (addresses)
	if len(newConfig.SrcAddr) == 0 {
		newConfig.SrcAddr = make([]byte, 16)
		copy(newConfig.SrcAddr, oldConfig.SrcAddr)
	}

	if len(newConfig.DstEther) == 0 {
		newConfig.DstEther = make([]byte, 6)
		copy(newConfig.DstEther, oldConfig.DstEther)
	}

	if len(newConfig.DstAddrMulticast) == 0 {
		newConfig.DstAddrMulticast = make([]byte, 16)
		copy(newConfig.DstAddrMulticast, oldConfig.DstAddrMulticast)
	}

	if len(newConfig.DstAddrUnicast) == 0 {
		newConfig.DstAddrUnicast = make([]byte, 16)
		copy(newConfig.DstAddrUnicast, oldConfig.DstAddrUnicast)
	}

	// Check ports
	if newConfig.PortMulticast == 0 {
		newConfig.PortMulticast = oldConfig.PortMulticast
	}

	if newConfig.PortUnicast == 0 {
		newConfig.PortUnicast = oldConfig.PortUnicast
	}

	// Check timeouts
	if newConfig.TcpSynAck == 0 {
		newConfig.TcpSynAck = oldConfig.TcpSynAck
	}

	if newConfig.TcpSyn == 0 {
		newConfig.TcpSyn = oldConfig.TcpSyn
	}

	if newConfig.TcpFin == 0 {
		newConfig.TcpFin = oldConfig.TcpFin
	}

	if newConfig.Tcp == 0 {
		newConfig.Tcp = oldConfig.Tcp
	}

	if newConfig.Udp == 0 {
		newConfig.Udp = oldConfig.Udp
	}

	if newConfig.Default == 0 {
		newConfig.Default = oldConfig.Default
	}

	cSyncConfig := ConvertPbToCSyncConfig(newConfig)
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
func (m *FwStateConfig) GetSyncConfig() *aclpb.SyncConfig {
	if m == nil || m.ptr.AsRawPtr() == nil {
		return nil
	}
	cSyncConfig := C.fwstate_config_get_sync_config(m.asCPModule())
	return ConvertCSyncConfigToPb(&cSyncConfig)
}

// GetMapConfig retrieves the map configuration from fwstate module
func (m *FwStateConfig) GetMapConfig() *aclpb.MapConfig {
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
	return &aclpb.MapConfig{
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
