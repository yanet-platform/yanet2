package balancer

// This module gives GO API to configure balancer module

//#cgo CFLAGS: -fsanitize=address,undefined
//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../
//#cgo CFLAGS: -I../../../build
//#cgo CFLAGS: -I../../../ -I../../../lib -I../../../common
//#cgo LDFLAGS: -L../../../build/modules/balancer/api -lbalancer_cp
//#cgo LDFLAGS: -L../../../build/modules/balancer/state -lbalancer_state
//#cgo LDFLAGS: -L../../../build/filter -lfilter
//#cgo LDFLAGS: -L../../../build/lib/logging -llogging
//#cgo LDFLAGS: -fsanitize=address,undefined
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
	"time"
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

func ptrToSlice(p *C.uint8_t, len int) []byte {
	return C.GoBytes(unsafe.Pointer(p), C.int(len))
}

func addressToSlice(p *C.uint8_t, addr C.int) []byte {
	len := 16
	if addr == C.IPPROTO_IP {
		len = 4
	}
	return ptrToSlice(p, len)
}

func vsProtoFromIpProto(proto C.int) TransportProto {
	if proto == C.IPPROTO_TCP {
		return TransportProtoTcp
	} else {
		return TransportProtoUdp
	}
}

////////////////////////////////////////////////////////////////////////////////
// Session Table
////////////////////////////////////////////////////////////////////////////////

// Table of the sessions between clients and real servers
type BalancerState struct {
	inner *C.struct_balancer_state
}

func NewState(
	agent *ffi.Agent,
	tableSize uint64,
	timeouts *SessionsTimeouts,
) (BalancerState, error) {
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

// Fills state info
func (state *BalancerState) Info() (*StateInfo, error) {
	// Get vs info

	vsInfo := C.struct_balancer_virtual_services_info{}
	res, err := C.balancer_fill_vs_info(state.inner, &vsInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to fill vs info: %w", err)
	}
	if res != 0 {
		return nil, fmt.Errorf("failed to fill vs info")
	}
	defer C.balancer_free_vs_info(state.inner, &vsInfo)

	// Get real info

	realsInfo := C.struct_balancer_reals_info{}
	res, err = C.balancer_fill_reals_info(state.inner, &realsInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to fill reals info: %w", err)
	}
	if res != 0 {
		return nil, fmt.Errorf("failed to fill reals info")
	}
	defer C.balancer_free_reals_info(state.inner, &realsInfo)

	return &StateInfo{
		VsInfo:   convertToVsInfo(&vsInfo),
		RealInfo: convertToRealsInfo(&realsInfo),
	}, nil
}

func convertToVsStats(stats *C.struct_balancer_vs_stats) VsStats {
	return VsStats{
		IncomingPackets:      uint64(stats.incoming_packets),
		IncomingBytes:        uint64(stats.incoming_bytes),
		PacketSrcNotAllowed:  uint64(stats.packet_src_not_allowed),
		NoReals:              uint64(stats.no_reals),
		OpsPackets:           uint64(stats.ops_packets),
		SessionTableOverflow: uint64(stats.session_table_overflow),
		RealIsDisabled:       uint64(stats.real_is_disabled),
		PacketNotRescheduled: uint64(stats.packet_not_rescheduled),
		CreatedSessions:      uint64(stats.created_sessions),
		OutgoingPackets:      uint64(stats.outgoing_packets),
		OutgoingBytes:        uint64(stats.outgoing_bytes),
	}
}

func convertToRealStats(stats *C.struct_balancer_real_stats) RealStats {
	return RealStats{
		RealDisabledPackets: uint64(stats.disabled),
		OpsPackets:          uint64(stats.ops_packets),
		CreatedSessions:     uint64(stats.created_sessions),
		SendPackets:         uint64(stats.packets),
		SendBytes:           uint64(stats.bytes),
	}
}

func convertToVsInfo(cVsInfo *C.struct_balancer_virtual_services_info) []StateVsInfo {
	vsInfo := make([]StateVsInfo, int(cVsInfo.count))
	for idx := range len(vsInfo) {
		curVsInfo := &vsInfo[idx]
		curCVsInfo := &unsafe.Slice((*C.struct_balancer_vs_info)(unsafe.Pointer(cVsInfo.info)), int(cVsInfo.count))[idx]
		*curVsInfo = StateVsInfo{
			Ip:                  addressToSlice(&curCVsInfo.ip[0], curCVsInfo.ip_proto),
			Port:                uint16(curCVsInfo.virtual_port),
			TransportProto:      vsProtoFromIpProto(curCVsInfo.transport_proto),
			ActiveSessions:      uint64(curCVsInfo.active_sessions),
			LastPacketTimestamp: time.Unix(int64(curCVsInfo.last_packet_timestamp), 0),
			Stats:               convertToVsStats(&curCVsInfo.stats),
		}
	}
	return vsInfo
}

func convertToRealsInfo(cRealsInfo *C.struct_balancer_reals_info) []StateRealInfo {
	realsInfo := make([]StateRealInfo, int(cRealsInfo.count))
	for idx := range len(realsInfo) {
		curRealInfo := &realsInfo[idx]
		curCRealInfo := &unsafe.Slice((*C.struct_balancer_real_info)(unsafe.Pointer(cRealsInfo.info)), int(cRealsInfo.count))[idx]
		*curRealInfo = StateRealInfo{
			Vip: addressToSlice(
				&curCRealInfo.vip[0],
				curCRealInfo.virtual_ip_proto,
			),
			VirtualPort:         uint16(curCRealInfo.virtual_port),
			RealIp:              addressToSlice(&curCRealInfo.ip[0], curCRealInfo.real_ip_proto),
			TransportProto:      vsProtoFromIpProto(curCRealInfo.transport_proto),
			ActiveSessions:      uint64(curCRealInfo.active_sessions),
			LastPacketTimestamp: time.Unix(int64(curCRealInfo.last_packet_timestamp), 0),
			Stats:               convertToRealStats(&curCRealInfo.stats),
		}
	}
	return realsInfo
}

////////////////////////////////////////////////////////////////////////////////

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

func (state *BalancerState) RealActiveSessionCount(realIdx uint64) (uint64, error) {
	info := C.struct_balancer_real_info{}
	ec, err := C.balancer_fill_real_info(
		state.inner, C.size_t(realIdx), &info)
	if err != nil {
		return 0, fmt.Errorf("failed to get real info: %w", err)
	}
	if ec != 0 {
		return 0, fmt.Errorf("failed to get real info: ec=%d", ec)
	}
	return uint64(info.active_sessions), nil
}

////////////////////////////////////////////////////////////////////////////////
// Virtual service config
////////////////////////////////////////////////////////////////////////////////

// Virtual service config
type VsConfig struct {
	inner *C.struct_balancer_vs_config
}

func realFlags(real *Real) uint64 {
	realFlags := 0
	if real.DstAddr.Is6() {
		realFlags |= C.BALANCER_REAL_IPV6_FLAG
	}
	if !real.Enabled {
		realFlags |= C.BALANCER_REAL_DISABLED_FLAG
	}
	return uint64(realFlags)
}

func vsFlags(vs *VirtualService) uint64 {
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
	if vs.Scheduler == VsSchedulerPRR || vs.Scheduler == VsSchedulerWLC {
		flags |= C.BALANCER_VS_PRR_FLAG
	}
	return uint64(flags)
}

// Create Virtual service config from `Virtual Service`
func (state *BalancerState) NewVsConfig(agent *ffi.Agent, vs *VirtualService) (VsConfig, error) {
	// make current wlc info
	if vs.Scheduler == VsSchedulerWLC {
		vs.Wlc = NewWlcInfo(10, 1024)
	} else {
		vs.Wlc = nil
	}

	flags := vsFlags(vs)

	proto := C.IPPROTO_TCP
	if vs.Proto == TransportProtoUdp {
		proto = C.IPPROTO_UDP
	}

	// register vs in balancer state

	idx, err := C.balancer_state_register_vs(
		state.inner,
		C.uint64_t(flags),
		sliceToPtr(vs.Address.AsSlice()),
		C.uint16_t(vs.Port),
		C.int(proto),
	)
	if err != nil {
		return VsConfig{inner: nil}, fmt.Errorf("failed to register vs: %w", err)
	}
	if idx == -1 {
		return VsConfig{inner: nil}, fmt.Errorf("failed to register vs")
	}
	vs.Idx = int64(idx)

	// create vs config
	config, err := C.balancer_vs_config_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		C.size_t(idx),
		(C.uint64_t)(flags),
		sliceToPtr(vs.Address.AsSlice()),
		(C.uint16_t)(vs.Port),
		(C.uint8_t)(proto),
		(C.size_t)(len(vs.Reals)),
		(C.size_t)(len(vs.AllowedSrc)),
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
			return VsConfig{
					inner: nil,
				}, fmt.Errorf(
					"failed to set %d-th allowed src: %w",
					idx+1,
					err,
				)
		}
	}

	// first, register all reals and update their info in WLC if needed
	for idx := range vs.Reals {
		real := &vs.Reals[idx]
		realFlags := realFlags(real)

		realIdx, err := C.balancer_state_register_real(
			state.inner,
			sliceToPtr(vs.Address.AsSlice()),
			C.uint64_t(flags),
			C.uint16_t(vs.Port),
			C.int(proto),
			C.uint64_t(realFlags),
			sliceToPtr(real.DstAddr.AsSlice()),
		)

		if err != nil {
			FreeVsConfig(&vsConfig)
			return VsConfig{inner: nil}, fmt.Errorf("failed to register real: %w", err)
		}
		if realIdx == -1 {
			FreeVsConfig(&vsConfig)
			return VsConfig{inner: nil}, fmt.Errorf("failed to register real")
		}

		real.Idx = int64(realIdx)

		// update info on WLC
		if vs.Wlc != nil {
			activeConnections, err := state.RealActiveSessionCount(uint64(real.Idx))
			if err != nil {
				FreeVsConfig(&vsConfig)
				return VsConfig{inner: nil}, fmt.Errorf("failed to get active session count for real %d: %w", real.Idx, err)
			}
			vs.Wlc.UpdateOrRegisterReal(uint64(vs.Idx), uint64(real.Idx), uint64(real.Weight), activeConnections, real.Enabled)
		}
	}

	if vs.Wlc != nil {
		// calculate WLC weights for real
		vs.Wlc.RecalculateWlcWeights()
	}

	for idx := range vs.Reals {
		real := &vs.Reals[idx]
		realFlags := realFlags(real)

		effectiveRealWeight := real.Weight
		if vs.Wlc != nil && real.Enabled {
			effectiveRealWeight = uint16(vs.Wlc.GetRealWlcWeight(uint64(real.Idx)))
		}

		_, err = C.balancer_vs_config_set_real(
			config,
			C.size_t(real.Idx),
			(C.size_t)(idx),
			(C.uint64_t)(realFlags),
			(C.uint16_t)(effectiveRealWeight),
			sliceToPtr(real.DstAddr.AsSlice()),
			sliceToPtr(real.SrcAddr.AsSlice()),
			sliceToPtr(real.SrcMask.AsSlice()),
		)
		if err != nil {
			FreeVsConfig(&vsConfig)
			return VsConfig{inner: nil}, fmt.Errorf("failed to set %d-th real: %w", idx+1, err)
		}
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
	for idx := range config.Services {
		vs := &config.Services[idx]
		vsConfig, err := state.NewVsConfig(agent, vs)
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

////////////////////////////////////////////////////////////////////////////////
