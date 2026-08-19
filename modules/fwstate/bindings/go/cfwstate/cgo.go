package cfwstate

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/modules/fwstate/api -lfwstate_cp
//#cgo LDFLAGS: -L../../../../../build/lib/counters -lcounters
//
//#include "api/agent.h"
//#include "modules/fwstate/api/fwstate_cp.h"
//#include "lib/fwstate/config.h"
//#include "lib/fwstate/fwmap.h"
//#include "lib/fwstate/fwstate_cursor.h"
//#include "lib/errors/errors.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the fwstate module configuration in
// shared memory.
type ModuleConfig struct {
	name       string
	ptr        ffi.ModuleConfig
	generation uint64
}

// DefaultSyncConfig returns the C-side default receive-side sync
// configuration, the baseline a fresh config starts from before the
// caller's request is merged over it.
func DefaultSyncConfig() SyncConfig {
	var cSyncConfig C.struct_fwstate_sync_config
	C.fwstate_config_set_defaults(&cSyncConfig)
	return newSyncConfigFromC(&cSyncConfig)
}

// NewModuleConfig creates an FWState module configuration fully built in
// one step: a config handle is constructed once and never updated
// afterwards.
//
// old names the config this one replaces, or nil for a fresh config.
// From old the sync config and the borrowed map offsets propagate; with
// old nil the maps are created fresh from mapConfig. When old is given,
// its maps are kept unless mapConfig asks for a different aligned size
// pair, in which case a new layer with the requested sizes is prepended.
//
// syncConfig is the final receive-side sync config to install, or nil to
// keep the propagated or default one. A zero workerCount leaves the
// config unmapped: no maps are created and the propagated chain, if
// any, is kept as is.
func NewModuleConfig(
	agent *ffi.Agent,
	name string,
	old *ModuleConfig,
	syncConfig *SyncConfig,
	mapConfig MapConfig,
	workerCount uint16,
) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cOld *C.struct_cp_module
	if old != nil {
		cOld = old.asRawPtr()
	}

	var cSync *C.struct_fwstate_sync_config
	if syncConfig != nil {
		cSyncConfig := syncConfig.toC()
		cSync = &cSyncConfig
	}

	var cErr *C.yanet_error
	ptr := C.fwstate_module_config_new(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		cOld,
		cSync,
		C.uint32_t(mapConfig.IndexSize),
		C.uint32_t(mapConfig.ExtraBucketCount),
		C.uint16_t(workerCount),
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize FWState module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	m := &ModuleConfig{
		name: name,
		ptr:  ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}

	// Generation mirrors the former CreateMaps bookkeeping: fresh maps
	// start at 1, a propagated config keeps the old generation and
	// advances it exactly when the map sizes changed.
	if old != nil {
		m.generation = old.generation
		if old.GetMapConfig() != m.GetMapConfig() {
			m.generation++
		}
	} else {
		m.generation = 1
	}

	return m, nil
}

func (m *ModuleConfig) Name() string {
	return m.name
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

func (m *ModuleConfig) Generation() uint64 {
	return m.generation
}

func (m *ModuleConfig) DetachMaps() {
	C.fwstate_module_config_detach_maps(m.asRawPtr())
}

// Free releases the underlying C memory.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (m *ModuleConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.fwstate_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}
