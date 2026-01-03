package ffi

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../../
//#cgo CFLAGS: -I../../../../build
//#cgo CFLAGS: -I../../../../ -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/balancer/api -lbalancer_cp
//#cgo LDFLAGS: -L../../../../build/modules/balancer/state -lbalancer_state
//#cgo LDFLAGS: -L../../../../build/filter -lfilter_compiler
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
*/
//
//#include "modules/balancer/api/vs.h"
//#include "modules/balancer/api/module.h"
//#include "modules/balancer/api/state.h"
//#include "modules/balancer/api/info.h"
//
// #include <netinet/in.h>
// #include <stdlib.h>
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/lib"
)

////////////////////////////////////////////////////////////////////////////////

type VsConfigPtr struct {
	inner *C.struct_balancer_vs_config
}

func (vsConfig *VsConfigPtr) Free() {
	C.balancer_vs_config_free(vsConfig.inner)
}

func (vsConfig *VsConfigPtr) IsNil() bool {
	return vsConfig.inner == nil
}

func (vsConfig VsConfigPtr) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(vsConfig.inner)
}

////////////////////////////////////////////////////////////////////////////////

func proto(virtualService *lib.VirtualService) C.uint8_t {
	switch virtualService.Identifier.Proto {
	case lib.ProtoTcp:
		return C.IPPROTO_TCP
	case lib.ProtoUdp:
		return C.IPPROTO_UDP
	default:
		panic("unknown proto")
	}
}

func flags(virtualService *lib.VirtualService) C.uint64_t {
	flags := 0
	id := &virtualService.Identifier
	if id.Ip.Is6() {
		flags |= C.BALANCER_VS_IPV6_FLAG
	}
	if virtualService.Flags.GRE {
		flags |= C.BALANCER_VS_GRE_FLAG
	}
	if virtualService.Flags.FixMSS {
		flags |= C.BALANCER_VS_FIX_MSS_FLAG
	}
	if virtualService.Flags.OPS {
		flags |= C.BALANCER_VS_OPS_FLAG
	}
	if virtualService.Flags.PureL3 {
		flags |= C.BALANCER_VS_PURE_L3_FLAG
	}
	if virtualService.Scheduler == lib.SchedulerPRR ||
		virtualService.Scheduler == lib.SchedulerWLC {
		// WLC -> PRR + least connections info update
		flags |= C.BALANCER_VS_PRR_FLAG
	}
	return C.uint64_t(flags)
}

func realFlags(real *lib.Real) C.uint64_t {
	realFlags := 0
	if real.Identifier.Ip.Is6() {
		realFlags |= C.BALANCER_REAL_IPV6_FLAG
	}
	if !real.Enabled {
		realFlags |= C.BALANCER_REAL_DISABLED_FLAG
	}
	return C.uint64_t(realFlags)
}

////////////////////////////////////////////////////////////////////////////////

// Create Virtual service config from `Virtual Service`
func NewVsConfig(
	agent ffi.Agent,
	virtualService *lib.VirtualService,
) (VsConfigPtr, error) {
	// Fill virtual service flags.
	flags := flags(virtualService)

	// Fill proto
	proto := proto(virtualService)

	// Get peers count
	peersIpv4, peersIpv6 := virtualService.PeersCount()

	// Create virtual service config
	cVsConfig, err := C.balancer_vs_config_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		C.size_t(virtualService.RegistryIdx),
		flags,
		sliceToPtr(virtualService.Identifier.Ip.AsSlice()),
		(C.uint16_t)(virtualService.Identifier.Port),
		proto,
		(C.size_t)(len(virtualService.Reals)),
		(C.size_t)(len(virtualService.AllowedSources)),
		(C.size_t)(peersIpv4),
		(C.size_t)(peersIpv6),
	)
	if err != nil {
		return VsConfigPtr{
				inner: nil,
			}, fmt.Errorf(
				"failed to create C virtual service config: %w",
				err,
			)
	}
	if cVsConfig == nil {
		return VsConfigPtr{
				inner: nil,
			}, fmt.Errorf(
				"failed to create C virtual service config",
			)
	}

	// Set allowed sources
	for idx, prefix := range virtualService.AllowedSources {
		startAddr := prefix.Addr()
		endAddr := xnetip.LastAddr(prefix)

		// Pin the slices to prevent GC from moving them during the C call
		startSlice := startAddr.AsSlice()
		endSlice := endAddr.AsSlice()

		_, err := C.balancer_vs_config_set_allowed_src_range(
			cVsConfig,
			(C.size_t)(idx),
			sliceToPtr(startSlice),
			sliceToPtr(endSlice),
		)
		if err != nil {
			C.balancer_vs_config_free(cVsConfig)
			return VsConfigPtr{
					inner: nil,
				}, fmt.Errorf(
					"failed to set allowed sources at index %d: %w",
					idx,
					err,
				)
		}
	}

	// Set peers
	peerV4Idx := 0
	peerV6Idx := 0
	for idx, peer := range virtualService.Peers {
		var err error
		if peer.Is4() {
			_, err = C.balancer_vs_config_set_peer_v4(
				cVsConfig,
				(C.size_t)(peerV4Idx),
				sliceToPtr(peer.AsSlice()),
			)
			peerV4Idx += 1
		} else { // ipv6
			_, err = C.balancer_vs_config_set_peer_v6(
				cVsConfig,
				(C.size_t)(peerV6Idx),
				sliceToPtr(peer.AsSlice()),
			)
			peerV6Idx += 1
		}
		if err != nil {
			C.balancer_vs_config_free(cVsConfig)
			return VsConfigPtr{
					inner: nil,
				}, fmt.Errorf(
					"failed to set peer at index %d: %w",
					idx,
					err,
				)
		}
	}

	// Set reals
	for idx := range virtualService.Reals {
		real := &virtualService.Reals[idx]
		flags := realFlags(real)
		weight := C.uint16_t(virtualService.RealConfigWeight(idx))
		_, err := C.balancer_vs_config_set_real(
			cVsConfig,
			(C.size_t)(real.RegistryIdx),
			(C.size_t)(idx),
			flags,
			C.uint16_t(weight),
			sliceToPtr(real.Identifier.Ip.AsSlice()),
			sliceToPtr(real.SrcAddr.AsSlice()),
			sliceToPtr(real.SrcMask.AsSlice()),
		)
		if err != nil {
			C.balancer_vs_config_free(cVsConfig)
			return VsConfigPtr{
					inner: nil,
				}, fmt.Errorf(
					"failed to set real at index %d: %w",
					idx,
					err,
				)
		}
	}

	// return vsConfig, err
	return VsConfigPtr{inner: cVsConfig}, nil
}
