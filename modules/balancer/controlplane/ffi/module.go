package ffi

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../../
//#cgo CFLAGS: -I../../../../build
//#cgo CFLAGS: -I../../../../ -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/balancer/api -lbalancer_cp
//#cgo LDFLAGS: -L../../../../build/modules/balancer/state -lbalancer_state
//#cgo LDFLAGS: -L../../../../build/filter -lfilter_compiler
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
//
//#include <stdlib.h>
//#include <string.h>
//#include <stdint.h>
//#include <netinet/in.h>
//
//#include "modules/balancer/api/vs.h"
//#include "modules/balancer/api/module.h"
//#include "modules/balancer/api/state.h"
//#include "modules/balancer/api/info.h"
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/lib"
)

////////////////////////////////////////////////////////////////////////////////

type ModuleConfigPtr struct {
	inner *C.struct_cp_module
}

func (config ModuleConfigPtr) Free() {
	C.balancer_module_config_free(config.inner)
}

////////////////////////////////////////////////////////////////////////////////

// Create new module config.
// Does not update controlplane modules.
func NewModuleConfig(
	agent ffi.Agent,
	name string,
	state ModuleConfigStatePtr,
	virtualServices []lib.VirtualService,
	addresses lib.BalancerAddresses,
	sessionsTimeouts lib.SessionsTimeouts,
) (ModuleConfigPtr, error) {
	// Name of the config
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	vsConfigs := []VsConfigPtr{}

	// Defer free of virtual service configs
	defer func() {
		for _, vs := range vsConfigs {
			vs.Free()
		}
	}()

	// Set virtual services configs
	for idx := range virtualServices {
		virtualService := &virtualServices[idx]
		vsConfigPtr, err := NewVsConfig(agent, virtualService)
		if err != nil {
			return ModuleConfigPtr{
					inner: nil,
				}, fmt.Errorf(
					"failed to create virtual service config at index %d: %w",
					idx,
					err,
				)
		}
		if vsConfigPtr.IsNil() {
			return ModuleConfigPtr{
					inner: nil,
				}, fmt.Errorf(
					"failed to create virtual service config at index %d",
					idx,
				)
		}
		vsConfigs = append(vsConfigs, vsConfigPtr)
	}

	// Prepare pointer to array of VS configs
	var vsConfigsPtr **C.struct_balancer_vs_config
	if len(vsConfigs) > 0 {
		// Create a C array of pointers to vs_config structs
		cArray := make([]*C.struct_balancer_vs_config, len(vsConfigs))
		for i := range vsConfigs {
			cArray[i] = vsConfigs[i].inner
		}
		vsConfigsPtr = &cArray[0]
	} else {
		vsConfigsPtr = nil
	}

	// Fill sessions timeouts
	cTimeouts := C.struct_balancer_sessions_timeouts{
		tcp_syn_ack: C.uint32_t(sessionsTimeouts.TcpSynAck),
		tcp_syn:     C.uint32_t(sessionsTimeouts.TcpSyn),
		tcp_fin:     C.uint32_t(sessionsTimeouts.TcpFin),
		tcp:         C.uint32_t(sessionsTimeouts.Tcp),
		udp:         C.uint32_t(sessionsTimeouts.Udp),
		def:         C.uint32_t(sessionsTimeouts.Default),
	}

	// Set balancer addresses

	// Set source addresses
	var sourceIpv4 C.struct_net4_addr
	var sourceIpv6 C.struct_net6_addr
	C.memcpy(
		unsafe.Pointer(&sourceIpv4.bytes[0]),
		unsafe.Pointer(&addresses.SourceIpV4[0]),
		C.size_t(4),
	)
	C.memcpy(
		unsafe.Pointer(&sourceIpv6.bytes[0]),
		unsafe.Pointer(&addresses.SourceIpV6[0]),
		C.size_t(16),
	)

	// Set decap addresses
	decapIpv4 := make([]C.struct_net4_addr, 0, len(addresses.DecapAddresses))
	decapIpv6 := make([]C.struct_net6_addr, 0, len(addresses.DecapAddresses))
	for _, addr := range addresses.DecapAddresses {
		if addr.Is4() {
			var ipv4 C.struct_net4_addr
			s := addr.AsSlice()
			C.memcpy(
				unsafe.Pointer(&ipv4.bytes[0]),
				unsafe.Pointer(&s[0]),
				C.size_t(4),
			)
			decapIpv4 = append(decapIpv4, ipv4)
		} else {
			var ipv6 C.struct_net6_addr
			s := addr.AsSlice()
			C.memcpy(unsafe.Pointer(&ipv6.bytes[0]), unsafe.Pointer(&s[0]), C.size_t(16))
			decapIpv6 = append(decapIpv6, ipv6)
		}
	}
	var decapIpv4Ptr *C.struct_net4_addr
	if len(decapIpv4) > 0 {
		decapIpv4Ptr = &decapIpv4[0]
	} else {
		decapIpv4Ptr = nil
	}
	var decapIpv6Ptr *C.struct_net6_addr
	if len(decapIpv6) > 0 {
		decapIpv6Ptr = &decapIpv6[0]
	} else {
		decapIpv6Ptr = nil
	}

	// Set balancer module config state
	cState := (*C.struct_balancer_state)(state.AsRawPtr())

	// Create cp_module
	cpModule, err := C.balancer_module_config_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		cState,
		&cTimeouts,
		(C.size_t)(len(vsConfigs)),
		vsConfigsPtr,
		&sourceIpv4,
		&sourceIpv6,
		(C.size_t)(len(decapIpv4)),
		decapIpv4Ptr,
		(C.size_t)(len(decapIpv6)),
		decapIpv6Ptr,
	)
	if err != nil {
		return ModuleConfigPtr{
				inner: nil,
			}, fmt.Errorf(
				"failed to create balancer module config: %w",
				err,
			)
	}
	if cpModule == nil {
		return ModuleConfigPtr{
				inner: nil,
			}, fmt.Errorf(
				"failed to create balancer module config",
			)
	}

	return ModuleConfigPtr{inner: cpModule}, nil
}

////////////////////////////////////////////////////////////////////////////////

func (config ModuleConfigPtr) UpdateShmModule(agent ffi.Agent) error {
	cfg := ffi.NewModuleConfig(unsafe.Pointer(config.inner))
	if err := agent.UpdateModules([]ffi.ModuleConfig{cfg}); err != nil {
		return fmt.Errorf("failed to update modules in shared memory: %w", err)
	}
	return nil
}
