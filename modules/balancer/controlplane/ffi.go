package balancer

// This module gives GO API to configure balancer module

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../
//#cgo CFLAGS: -I../../../build
//#cgo CFLAGS: -I../../../ -I../../../lib -I../../../common
//#cgo LDFLAGS: -L../../../build/modules/balancer/api -lbalancer_cp
//#cgo LDFLAGS: -L../../../build/filter -lfilter
//#cgo LDFLAGS: -L../../../build/lib/logging -llogging
/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
*/
//
//#include "modules/balancer/api/vs.h"
//#include "modules/balancer/api/module.h"
//#include "modules/balancer/api/session_table.h"
//#include "modules/balancer/api/session.h"
//
// #include <netinet/in.h>
// #include <stdlib.h>
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

////////////////////////////////////////////////////////////////////////////////
// Utils
////////////////////////////////////////////////////////////////////////////////

func sliceToPtr(s []byte) *C.uint8_t {
	return (*C.uint8_t)(&s[0])
}

////////////////////////////////////////////////////////////////////////////////
// Session Table
////////////////////////////////////////////////////////////////////////////////

// Table of the sessions between clients and real servers
type SessionTable struct {
	inner *C.struct_balancer_session_table
}

func NewSessionTable(agent *ffi.Agent, size uint64) (SessionTable, error) {
	table, err := C.balancer_session_table_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		C.size_t(size),
	)
	if err != nil {
		return SessionTable{inner: nil}, fmt.Errorf("failed to create session table: %w", err)
	}
	if table == nil {
		return SessionTable{inner: nil}, fmt.Errorf("failed to create session table")
	}
	return SessionTable{inner: table}, nil
}

// Free memory occupied by the session table
func FreeSessionTable(table *SessionTable) {
	C.balancer_session_table_free(table.inner)
}

// Extend session table on demand (use `force` to force extension)
func ExtendSessionTable(table *SessionTable, force bool) error {
	_, err := C.balancer_session_table_extend(table.inner, (C.bool)(force))
	return err
}

// Free memory unused by session table
func FreeUnusedInSessionTable(table *SessionTable) error {
	_, err := C.balancer_session_table_free_unused(table.inner)
	return err
}

////////////////////////////////////////////////////////////////////////////////
// Virtual service config
////////////////////////////////////////////////////////////////////////////////

// Virtual service config
type VsConfig struct {
	inner *C.struct_balancer_vs_config
}

// Create Virtual service config from `Virtual Service` (only enabled reals will be used)
func NewVsConfig(agent *ffi.Agent, vs *VirtualService) (VsConfig, error) {
	flags := 0
	if vs.Address.Is6() {
		flags |= C.BALANCER_VS_IPV6_FLAG
	}
	if vs.Flags.GRE {
		flags |= C.BALANCER_VS_GRE_FLAG
	}
	if vs.Flags.FixMSS {
		flags |= C.BALANCER_VS_FIX_MSS_FLAG
	}
	if vs.Flags.OPS {
		flags |= C.BALANCER_VS_OPS_FLAG
	}
	if vs.Flags.PureL3 {
		flags |= C.BALANCER_VS_PURE_L3_FLAG
	}
	proto := C.IPPROTO_TCP
	if vs.Proto == VsProtoUdp {
		proto = C.IPPROTO_UDP
	}

	// Add to config only enabled reals
	enabledReals := 0
	for _, real := range vs.Reals {
		if real.Enabled {
			enabledReals += 1
		}
	}
	config, err := C.balancer_vs_config_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		(C.uint64_t)(flags),
		sliceToPtr(vs.Address.AsSlice()),
		(C.uint16_t)(vs.Port),
		(C.uint8_t)(proto),
		(C.size_t)(len(vs.AllowedSrc)),
		(C.size_t)(enabledReals),
	)
	if err != nil {
		return VsConfig{inner: nil}, fmt.Errorf("failed to create vs config: %w", err)
	}
	if config == nil {
		return VsConfig{inner: nil}, fmt.Errorf("failed to create vs config")
	}
	vsConfig := VsConfig{
		inner: config,
	}
	for idx, prefix := range vs.AllowedSrc {
		startAddr := prefix.Addr()
		endAddr := xnetip.LastAddr(prefix)
		_, err := C.balancer_vs_config_set_allowed_src_range(
			config,
			(C.size_t)(idx),
			sliceToPtr(startAddr.AsSlice()),
			sliceToPtr(endAddr.AsSlice()),
		)
		if err != nil {
			FreeVsConfig(&vsConfig)
			return VsConfig{inner: nil}, fmt.Errorf("failed to set %d-th allowed src: %w", idx+1, err)
		}
	}

	// Add to config only enabled reals
	counter := 0
	for idx, real := range vs.Reals {
		if !real.Enabled {
			continue
		}
		realFlags := 0
		if real.DstAddr.Is6() {
			realFlags |= C.BALANCER_REAL_IPV6_FLAG
		}
		_, err := C.balancer_vs_config_set_real(
			config,
			(C.size_t)(counter),
			(C.uint64_t)(flags),
			(C.uint16_t)(real.Weight),
			sliceToPtr(real.DstAddr.AsSlice()),
			sliceToPtr(real.SrcAddr.AsSlice()),
			sliceToPtr(real.SrcMask.AsSlice()),
		)
		if err != nil {
			FreeVsConfig(&vsConfig)
			return VsConfig{inner: nil}, fmt.Errorf("failed to set %d-th real: %w", idx+1, err)
		}
		counter += 1
	}

	return vsConfig, err
}

func FreeVsConfig(config *VsConfig) {
	C.balancer_vs_config_free(config.inner)
}

////////////////////////////////////////////////////////////////////////////////
// Module config API
////////////////////////////////////////////////////////////////////////////////

type ModuleConfig struct {
	inner *C.struct_cp_module
}

// Create new `cp_module`
// No update dataplane modules
func NewModuleConfig(
	agent *ffi.Agent,
	sessionTable *SessionTable,
	config *ModuleInstanceConfig,
	name string,
) (ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	vsConfigs := []*C.struct_balancer_vs_config{}
	defer func() {
		for _, vs := range vsConfigs {
			FreeVsConfig(&VsConfig{inner: vs})
		}
	}()
	for _, vs := range config.Services {
		vsConfig, err := NewVsConfig(agent, &vs)
		if err != nil {
			return ModuleConfig{
					inner: nil,
				}, fmt.Errorf(
					"failed to create virtual service config: %w",
					err,
				)
		}
		vsConfigs = append(vsConfigs, vsConfig.inner)
	}
	timeouts, err := C.balancer_sessions_timeouts_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		(C.uint32_t)(config.SessionTimeouts.TcpSynAck),
		(C.uint32_t)(config.SessionTimeouts.TcpSyn),
		(C.uint32_t)(config.SessionTimeouts.TcpFin),
		(C.uint32_t)(config.SessionTimeouts.Tcp),
		(C.uint32_t)(config.SessionTimeouts.Udp),
		(C.uint32_t)(config.SessionTimeouts.Default),
	)
	if err != nil {
		return ModuleConfig{
				inner: nil,
			}, fmt.Errorf(
				"failed to create sessions timeouts: %w",
				err,
			)
	}
	if timeouts == nil {
		return ModuleConfig{
				inner: nil,
			}, fmt.Errorf(
				"failed to create sessions timeouts",
			)
	}

	cpModule, err := C.balancer_module_config_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		sessionTable.inner,
		(C.size_t)(len(vsConfigs)),
		(**C.struct_balancer_vs_config)(&vsConfigs[0]),
		timeouts,
	)
	if err != nil {
		return ModuleConfig{
				inner: nil,
			}, fmt.Errorf(
				"failed to create balancer module config: %w",
				err,
			)
	}
	if cpModule == nil {
		return ModuleConfig{inner: nil}, fmt.Errorf("failed to create balancer module config")
	}
	return ModuleConfig{inner: cpModule}, nil
}

func FreeModuleConfig(config *ModuleConfig) {
	C.balancer_module_config_free(config.inner)
}

func (cpModule *ModuleConfig) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(cpModule.inner)
}

func (config *ModuleConfig) InsertIntoRegistry(agent *ffi.Agent) error {
	cfg := ffi.NewModuleConfig(config.AsRawPtr())
	if err := agent.UpdateModules([]ffi.ModuleConfig{cfg}); err != nil {
		return fmt.Errorf("failed to update dp modules: %w", err)
	}
	return nil
}
