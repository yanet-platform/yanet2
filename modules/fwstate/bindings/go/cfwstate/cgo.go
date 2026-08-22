package cfwstate

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/modules/fwstate/api -lfwstate_cp
//#cgo LDFLAGS: -L../../../../../build/objects/fwstate/api -lfwstate_objects
//#cgo LDFLAGS: -L../../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../../build/lib/dataplane/config -lconfig_dp
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
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the fwstate module configuration in
// shared memory.
type ModuleConfig struct {
	name string
	ptr  ffi.ModuleConfig
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
// syncConfig is the final receive-side sync config to install, or nil to
// keep the defaults. fw4MapName and fw6MapName name standalone
// fwstate_map_v4 / fwstate_map_v6 objects whose fwtables the module
// inserts synced state into; an empty name declares no link and that
// family's table offset stays NULL, so the module counts and drops that
// family's sync frames without inserting.
func NewModuleConfig(
	agent *ffi.Agent,
	name string,
	syncConfig *SyncConfig,
	fw4MapName, fw6MapName string,
) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cSync *C.struct_fwstate_sync_config
	if syncConfig != nil {
		cSyncConfig := syncConfig.toC()
		cSync = &cSyncConfig
	}

	var cFw4Name *C.char
	if fw4MapName != "" {
		cFw4Name = C.CString(fw4MapName)
		defer C.free(unsafe.Pointer(cFw4Name))
	}

	var cFw6Name *C.char
	if fw6MapName != "" {
		cFw6Name = C.CString(fw6MapName)
		defer C.free(unsafe.Pointer(cFw6Name))
	}

	var cErr *C.yanet_error
	ptr := C.fwstate_module_config_new(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		cSync,
		cFw4Name,
		cFw6Name,
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize FWState module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &ModuleConfig{
		name: name,
		ptr:  ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
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

// Free destroys the module config when it is dangling — referenced by no live
// configuration generation — and reports nil. While a live generation
// still references it the free is refused with ffi.ErrStillReferenced
// and the handle stays usable: the caller must remember it and free it
// again once the generations holding it drain. Safe to call multiple
// times: subsequent calls are no-ops reporting nil.
func (m *ModuleConfig) Free() error {
	ptr := m.asRawPtr()
	if ptr == nil {
		return nil
	}
	var cErr *C.yanet_error
	rc, errno := C.fwstate_module_config_free(ptr, &cErr)
	if rc == 0 {
		m.ptr = ffi.ModuleConfig{}
		return nil
	}
	if errors.Is(errno, syscall.EAGAIN) {
		// The refused attempt allocated an error chain; release it
		// rather than leaking one per attempt. The object is intact.
		C.yanet_error_free(cErr)
		return ffi.ErrStillReferenced
	}
	return fmt.Errorf("failed to free module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
}
