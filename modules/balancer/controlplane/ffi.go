package balancer

// This module gives GO API to configure balancer module

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../
//#cgo CFLAGS: -I../../../build
//#cgo CFLAGS: -I../../../ -I../../../lib -I../../../common
//#cgo LDFLAGS: -L../../../build/modules/balancer/api -lbalancer_cp
//#cgo LDFLAGS: -L../../../build/modules/balancer/state -lbalancer_state
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
//#include "modules/balancer/api/state.h"
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
type BalancerState struct {
	inner *C.struct_balancer_state
}

func NewState(agent *ffi.Agent, tableSize uint64, timeouts *SessionsTimeouts) (BalancerState, error) {
	state, err := C.balancer_state_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		C.size_t(tableSize),
		C.uint32_t(timeouts.TcpSynAck),
		C.uint32_t(timeouts.TcpSyn),
		C.uint32_t(timeouts.TcpFin),
		C.uint32_t(timeouts.Tcp),
		C.uint32_t(timeouts.Udp),
		C.uint32_t(timeouts.Default),
	)
	if err != nil {
		return BalancerState{inner: nil}, fmt.Errorf("failed to create state: %w", err)
	}
	if state == nil {
		return BalancerState{inner: nil}, fmt.Errorf("failed to create state")
	}
	return BalancerState{inner: state}, nil
}

// Free memory occupied by the session table
func (state *BalancerState) Free() {
	C.balancer_state_destroy(state.inner)
}

// Extend session table on demand (use `force` to force extension)
func (state *BalancerState) ExtendSessionTable(force bool) error {
	_, err := C.balancer_state_extend_session_table(state.inner, (C.bool)(force))
	return err
}

// Free memory unused by session table
func (state *BalancerState) FreeUnusedInSessionTable() error {
	_, err := C.balancer_state_gc_session_table(state.inner)
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
func (state *BalancerState) NewVsConfig(agent *ffi.Agent, vs *VirtualService) (VsConfig, error) {
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

	// register vs in balancer state

	idx, err := C.balancer_state_register_vs(
		state.inner,
		C.uint64_t(flags),
		sliceToPtr(vs.Address.AsSlice()),
		C.uint16_t(vs.Port),
		C.uint8_t(proto),
	)
	if err != nil {
		return VsConfig{inner: nil}, fmt.Errorf("failed to register vs: %w", err)
	}
	if idx == -1 {
		return VsConfig{inner: nil}, fmt.Errorf("failed to register vs")
	}

	// create vs config
	config, err := C.balancer_vs_config_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		C.size_t(idx),
		(C.uint64_t)(flags),
		sliceToPtr(vs.Address.AsSlice()),
		(C.uint16_t)(vs.Port),
		(C.uint8_t)(proto),
		(C.size_t)(len(vs.AllowedSrc)),
		(C.size_t)(len(vs.Reals)),
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
		realFlags := 0
		if real.DstAddr.Is6() {
			realFlags |= C.BALANCER_REAL_IPV6_FLAG
		}
		if !real.Enabled {
			realFlags |= C.BALANCER_REAL_DISABLED_FLAG
		}

		// register real

		realIdx, err := C.balancer_state_register_real(
			state.inner,
			C.uint64_t(realFlags),
			sliceToPtr(real.DstAddr.AsSlice()),
			C.uint8_t(proto),
		)
		if err != nil {
			FreeVsConfig(&vsConfig)
			return VsConfig{inner: nil}, fmt.Errorf("failed to register real: %w", err)
		}
		if realIdx == -1 {
			FreeVsConfig(&vsConfig)
			return VsConfig{inner: nil}, fmt.Errorf("failed to register real")
		}

		_, err = C.balancer_vs_config_set_real(
			config,
			C.size_t(realIdx),
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
func (state *BalancerState) NewModuleConfig(
	agent *ffi.Agent,
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
		vsConfig, err := state.NewVsConfig(agent, &vs)
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

	cpModule, err := C.balancer_module_config_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		state.inner,
		(C.size_t)(len(vsConfigs)),
		(**C.struct_balancer_vs_config)(&vsConfigs[0]))
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

func (config *ModuleConfig) Free() {
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
