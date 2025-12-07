package ffi

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../../
//#cgo CFLAGS: -I../../../../build
//#cgo CFLAGS: -I../../../../ -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/balancer/api -lbalancer_cp
//#cgo LDFLAGS: -L../../../../build/modules/balancer/state -lbalancer_state
//#cgo LDFLAGS: -L../../../../build/filter -lfilter
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
	"time"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
)

////////////////////////////////////////////////////////////////////////////////

// State of the balancer module config, composed of session table,
// sessions timeouts description and registry of virtual and real services.
// Some services may not be used in the current config.
type ModuleConfigStatePtr struct {
	inner *C.struct_balancer_state
}

func (moduleConfig ModuleConfigStatePtr) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(moduleConfig.inner)
}

// Free memory occupied by the balancer module config state.
func (state *ModuleConfigStatePtr) Free() {
	C.balancer_state_destroy(state.inner)
}

////////////////////////////////////////////////////////////////////////////////

// Create new state of the balancer with provided
// session table size and timeouts.
func NewModuleConfigState(
	agent ffi.Agent,
	initialTableSize uint,
) (ModuleConfigStatePtr, error) {
	state, err := C.balancer_state_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		C.size_t(initialTableSize),
	)
	if err != nil {
		return ModuleConfigStatePtr{inner: nil}, fmt.Errorf("failed to create state: %w", err)
	}
	if state == nil {
		return ModuleConfigStatePtr{inner: nil}, fmt.Errorf("failed to create state")
	}
	return ModuleConfigStatePtr{inner: state}, nil
}

// Extend session table on demand (use `force` to force extension).
func (state *ModuleConfigStatePtr) ExtendSessionTable(force bool) error {
	_, err := C.balancer_state_extend_session_table(state.inner, (C.bool)(force))
	return err
}

// Free memory unused by balancer session state.
func (state *ModuleConfigStatePtr) FreeUnusedInSessionTable() error {
	_, err := C.balancer_state_gc_session_table(state.inner)
	return err
}

////////////////////////////////////////////////////////////////////////////////

// Register virtual service in the module state registry.
func (state *ModuleConfigStatePtr) RegisterVs(id *module.VsIdentifier) (uint, error) {
	networkProto := addrToIpProto(&id.Ip)
	transportProto := transportProtoToIpProto(id.Proto)
	port := C.uint16_t(id.Port)
	vsIp := sliceToPtr(id.Ip.AsSlice())
	idx, err := C.balancer_state_register_vs(state.inner, transportProto, networkProto, vsIp, port)
	if err != nil {
		return 0, fmt.Errorf("failed to register real: %w", err)
	}
	if int(idx) == -1 {
		return 0, fmt.Errorf("failed to register real")
	}
	return uint(idx), nil
}

// Register real in the module state registry.
func (state *ModuleConfigStatePtr) RegisterReal(id *module.RealIdentifier) (uint, error) {
	vsNetworkProto := addrToIpProto(&id.Vs.Ip)
	transportProto := transportProtoToIpProto(id.Vs.Proto)
	realNetworkProto := addrToIpProto(&id.Ip)
	vsIp := sliceToPtr(id.Vs.Ip.AsSlice())
	realIp := sliceToPtr(id.Ip.AsSlice())
	port := C.uint16_t(id.Vs.Port)
	idx, err := C.balancer_state_register_real(state.inner, transportProto, vsNetworkProto, vsIp, port, realNetworkProto, realIp)
	if err != nil {
		return 0, fmt.Errorf("failed to register real: %w", err)
	}
	if int(idx) == -1 {
		return 0, fmt.Errorf("failed to register real")
	}
	return uint(idx), nil
}

////////////////////////////////////////////////////////////////////////////////

func vsStatsFromC(s *C.struct_balancer_vs_stats) module.VsStats {
	return module.VsStats{
		IncomingPackets:        uint64(s.incoming_packets),
		IncomingBytes:          uint64(s.incoming_bytes),
		PacketSrcNotAllowed:    uint64(s.packet_src_not_allowed),
		NoReals:                uint64(s.no_reals),
		OpsPackets:             uint64(s.ops_packets),
		SessionTableOverflow:   uint64(s.session_table_overflow),
		EchoIcmpPackets:        uint64(s.echo_icmp_packets),
		ErrorIcmpPackets:       uint64(s.error_icmp_packets),
		RealIsDisabled:         uint64(s.real_is_disabled),
		RealIsRemoved:          uint64(s.real_is_removed),
		NotRescheduledPackets:  uint64(s.not_rescheduled_packets),
		BroadcastedIcmpPackets: uint64(s.broadcasted_icmp_packets),
		CreatedSessions:        uint64(s.created_sessions),
		OutgoingPackets:        uint64(s.outgoing_packets),
		OutgoingBytes:          uint64(s.outgoing_bytes),
	}
}

func realStatsFromC(s *C.struct_balancer_real_stats) module.RealStats {
	return module.RealStats{
		PacketsRealDisabled:   uint64(s.packets_real_disabled),
		PacketsRealNotPresent: uint64(s.packets_real_not_present),
		OpsPackets:            uint64(s.ops_packets),
		ErrorIcmpPackets:      uint64(s.error_icmp_packets),
		CreatedSessions:       uint64(s.created_sessions),
		Packets:               uint64(s.packets),
		Bytes:                 uint64(s.bytes),
	}
}

// VirtualServicesInfo returns info for all VSes registered in state.
func (state *ModuleConfigStatePtr) VirtualServicesInfo() []module.VsInfo {
	var info C.struct_balancer_virtual_services_info
	rc, err := C.balancer_fill_virtual_services_info(state.inner, &info)
	if err != nil || int(rc) != 0 {
		return nil
	}
	defer C.balancer_free_virtual_services_info(state.inner, &info)

	count := int(info.count)
	if count == 0 || info.info == nil {
		return nil
	}
	cArr := unsafe.Slice((*C.struct_balancer_virtual_service_info)(info.info), count)
	out := make([]module.VsInfo, count)
	for i := range count {
		entry := cArr[i]
		addr := ipFromC(&entry.ip[0], entry.ip_proto)
		id := module.VsIdentifier{
			Ip:    addr,
			Port:  uint16(entry.virtual_port),
			Proto: moduleProtoFromC(entry.transport_proto),
		}
		stats := vsStatsFromC(&entry.stats)

		out[i] = module.VsInfo{
			VsRegistryIdx:       uint(i),
			VsIdentifier:        id,
			ActiveSessions:      uint64(entry.active_sessions),
			LastPacketTimestamp: time.Unix(int64(entry.last_packet_timestamp), 0),
			Stats:               stats,
		}
	}
	return out
}

// RealsInfo returns info for all reals registered in state.
func (state *ModuleConfigStatePtr) RealsInfo() []module.RealInfo {
	var info C.struct_balancer_reals_info
	rc, err := C.balancer_fill_reals_info(state.inner, &info)
	if err != nil || int(rc) != 0 {
		return nil
	}
	defer C.balancer_free_reals_info(state.inner, &info)

	count := int(info.count)
	if count == 0 || info.info == nil {
		return nil
	}
	cArr := unsafe.Slice((*C.struct_balancer_real_info)(info.info), count)
	out := make([]module.RealInfo, count)
	for i := range count {
		entry := cArr[i]
		vip := ipFromC(&entry.vip[0], entry.virtual_ip_proto)
		realIp := ipFromC(&entry.ip[0], entry.real_ip_proto)
		vsId := module.VsIdentifier{
			Ip:    vip,
			Port:  uint16(entry.virtual_port),
			Proto: moduleProtoFromC(entry.transport_proto),
		}
		realId := module.RealIdentifier{
			Vs: vsId,
			Ip: realIp,
		}
		stats := realStatsFromC(&entry.stats)

		out[i] = module.RealInfo{
			RealRegistryIdx:     uint(i),
			RealIdentifier:      realId,
			ActiveSessions:      uint64(entry.active_sessions),
			LastPacketTimestamp: time.Unix(int64(entry.last_packet_timestamp), 0),
			Stats:               stats,
		}
	}
	return out
}

// VirtualServiceInfo returns info for a single VS by registry index.
func (state *ModuleConfigStatePtr) VirtualServiceInfo(idx uint) *module.VsInfo {
	var entry C.struct_balancer_virtual_service_info
	rc, err := C.balancer_fill_virtual_service_info(state.inner, C.size_t(idx), &entry)
	if err != nil || int(rc) != 0 {
		return nil
	}
	addr := ipFromC(&entry.ip[0], entry.ip_proto)
	id := module.VsIdentifier{
		Ip:    addr,
		Port:  uint16(entry.virtual_port),
		Proto: moduleProtoFromC(entry.transport_proto),
	}
	stats := vsStatsFromC(&entry.stats)

	out := module.VsInfo{
		VsRegistryIdx:       idx,
		VsIdentifier:        id,
		ActiveSessions:      uint64(entry.active_sessions),
		LastPacketTimestamp: time.Unix(int64(entry.last_packet_timestamp), 0),
		Stats:               stats,
	}
	return &out
}

// RealInfo returns info for a single real by registry index.
func (state *ModuleConfigStatePtr) RealInfo(idx uint) *module.RealInfo {
	var entry C.struct_balancer_real_info
	rc, err := C.balancer_fill_real_info(state.inner, C.size_t(idx), &entry)
	if err != nil || int(rc) != 0 {
		return nil
	}
	vip := ipFromC(&entry.vip[0], entry.virtual_ip_proto)
	realIp := ipFromC(&entry.ip[0], entry.real_ip_proto)
	vsId := module.VsIdentifier{
		Ip:    vip,
		Port:  uint16(entry.virtual_port),
		Proto: moduleProtoFromC(entry.transport_proto),
	}
	realId := module.RealIdentifier{
		Vs: vsId,
		Ip: realIp,
	}
	stats := realStatsFromC(&entry.stats)

	out := module.RealInfo{
		RealRegistryIdx:     idx,
		RealIdentifier:      realId,
		ActiveSessions:      uint64(entry.active_sessions),
		LastPacketTimestamp: time.Unix(int64(entry.last_packet_timestamp), 0),
		Stats:               stats,
	}
	return &out
}
