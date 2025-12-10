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
	"net/netip"
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

func (state *ModuleConfigStatePtr) SessionTableCapacity() uint {
	return uint(C.balancer_state_session_table_capacity(state.inner))
}

////////////////////////////////////////////////////////////////////////////////

// Create new state of the balancer with provided
// session table size and timeouts.
func NewModuleConfigState(
	agent ffi.Agent,
	initialTableSize uint,
) (ModuleConfigStatePtr, error) {
	if initialTableSize == 0 {
		return ModuleConfigStatePtr{
			inner: nil,
		}, fmt.Errorf("initial table size must be greater than 0")
	}
	state, err := C.balancer_state_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		C.size_t(initialTableSize),
	)
	if err != nil {
		return ModuleConfigStatePtr{
				inner: nil,
			}, fmt.Errorf(
				"failed to create state: %w",
				err,
			)
	}
	if state == nil {
		return ModuleConfigStatePtr{
				inner: nil,
			}, fmt.Errorf(
				"failed to create state",
			)
	}
	return ModuleConfigStatePtr{inner: state}, nil
}

// Extend session table on demand (use `force` to force extension).
func (state *ModuleConfigStatePtr) ResizeSessionTable(newSize uint) (bool, error) {
	ec, err := C.balancer_state_resize_session_table(
		state.inner,
		C.size_t(newSize),
	)
	if err != nil {
		return false, fmt.Errorf("failed to resize session table: %w", err)
	}
	if ec == -1 {
		return false, fmt.Errorf("failed to resize session table: memory not enough")
	}
	return ec == 1, nil
}

// Free memory unused by balancer session state.
func (state *ModuleConfigStatePtr) FreeUnusedInSessionTable() (bool, error) {
	ec, err := C.balancer_state_gc_session_table(state.inner)
	if err != nil {
		return false, fmt.Errorf("failed to free unused in session table: %w", err)
	}
	if ec == -1 {
		return false, fmt.Errorf("failed to free unused in session table")
	}
	return ec == 1, nil
}

////////////////////////////////////////////////////////////////////////////////

// Register virtual service in the module state registry.
func (state *ModuleConfigStatePtr) RegisterVs(
	id *module.VsIdentifier,
) (uint, error) {
	networkProto := addrToIpProto(&id.Ip)
	transportProto := transportProtoToIpProto(id.Proto)
	port := C.uint16_t(id.Port)
	vsIp := sliceToPtr(id.Ip.AsSlice())
	idx, err := C.balancer_state_register_vs(
		state.inner,
		transportProto,
		networkProto,
		vsIp,
		port,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to register real: %w", err)
	}
	if int(idx) == -1 {
		return 0, fmt.Errorf("failed to register real")
	}
	return uint(idx), nil
}

// Register real in the module state registry.
func (state *ModuleConfigStatePtr) RegisterReal(
	id *module.RealIdentifier,
) (uint, error) {
	vsNetworkProto := addrToIpProto(&id.Vs.Ip)
	transportProto := transportProtoToIpProto(id.Vs.Proto)
	realNetworkProto := addrToIpProto(&id.Ip)
	vsIp := sliceToPtr(id.Vs.Ip.AsSlice())
	realIp := sliceToPtr(id.Ip.AsSlice())
	port := C.uint16_t(id.Vs.Port)
	idx, err := C.balancer_state_register_real(
		state.inner,
		transportProto,
		vsNetworkProto,
		vsIp,
		port,
		realNetworkProto,
		realIp,
	)
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
	cArr := unsafe.Slice(
		(*C.struct_balancer_virtual_service_info)(info.info),
		count,
	)
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
			VsRegistryIdx: uint(i),
			VsIdentifier:  id,
			LastPacketTimestamp: time.Unix(
				int64(entry.last_packet_timestamp),
				0,
			),
			Stats: stats,
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
			RealRegistryIdx: uint(i),
			RealIdentifier:  realId,
			LastPacketTimestamp: time.Unix(
				int64(entry.last_packet_timestamp),
				0,
			),
			Stats: stats,
		}
	}
	return out
}

// VirtualServiceInfo returns info for a single VS by registry index.
func (state *ModuleConfigStatePtr) VirtualServiceInfo(idx uint) *module.VsInfo {
	var entry C.struct_balancer_virtual_service_info
	rc, err := C.balancer_fill_virtual_service_info(
		state.inner,
		C.size_t(idx),
		&entry,
	)
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
		LastPacketTimestamp: time.Unix(int64(entry.last_packet_timestamp), 0),
		Stats:               stats,
	}
	return &out
}

////////////////////////////////////////////////////////////////////////////////

// SessionsInfo returns info about active sessions in the balancer.
// If countOnly is true, only the count is returned without session details.
// The now parameter should be the current timestamp in seconds.
func (state *ModuleConfigStatePtr) SessionsInfo(
	now uint32,
	countOnly bool,
) *module.SessionsInfo {
	var info C.struct_balancer_sessions_info
	rc, err := C.balancer_fill_sessions_info(
		state.inner,
		&info,
		C.uint32_t(now),
		C.bool(countOnly),
	)
	if err != nil || int(rc) != 0 {
		return nil
	}
	defer C.balancer_free_sessions_info(state.inner, &info)

	count := uint(info.count)
	result := &module.SessionsInfo{
		SessionsCount: count,
	}

	if countOnly || count == 0 || info.sessions == nil {
		return result
	}

	cArr := unsafe.Slice(
		(*C.struct_balancer_session_info)(info.sessions),
		count,
	)
	result.Sessions = make([]module.SessionInfo, count)

	for i := range count {
		entry := cArr[i]

		// Get VS info to determine IP protocol for client IP
		vsInfo := state.VirtualServiceInfo(uint(entry.vs_id))
		var clientIp netip.Addr
		var ipProto C.int = C.IPPROTO_IP // default to IPv4
		if vsInfo != nil {
			ipProto = addrToIpProto(&vsInfo.VsIdentifier.Ip)
		}
		clientIp = ipFromC(&entry.client_ip[0], ipProto)

		// Get Real info to build RealIdentifier
		realInfo := state.RealInfo(uint(entry.real_id))
		var realId module.RealIdentifier
		if realInfo != nil {
			realId = realInfo.RealIdentifier
		}

		result.Sessions[i] = module.SessionInfo{
			ClientAddr:      clientIp,
			ClientPort:      uint16(entry.client_port),
			Real:            realId,
			CreateTimestamp: time.Unix(int64(entry.create_timestamp), 0),
			LastPacketTimestamp: time.Unix(
				int64(entry.last_packet_timestamp),
				0,
			),
			Timeout: time.Duration(entry.timeout) * time.Second,
		}
	}

	return result
}

////////////////////////////////////////////////////////////////////////////////

// BalancerInfo returns complete info about the balancer state including
// module stats, virtual services info, and reals info.
func (state *ModuleConfigStatePtr) BalancerInfo() *module.BalancerInfo {
	var info C.struct_balancer_info
	rc, err := C.balancer_fill_info(state.inner, &info)
	if err != nil || int(rc) != 0 {
		return nil
	}
	defer C.balancer_free_info(state.inner, &info)

	result := &module.BalancerInfo{}

	// Fill module stats
	result.Module = module.ModuleStats{
		L4: module.L4Stats{
			IncomingPackets:  uint64(info.stats.l4.incoming_packets),
			SelectVSFailed:   uint64(info.stats.l4.select_vs_failed),
			InvalidPackets:   uint64(info.stats.l4.invalid_packets),
			SelectRealFailed: uint64(info.stats.l4.select_real_failed),
			OutgoingPackets:  uint64(info.stats.l4.outgoing_packets),
		},
		ICMPv4: module.ICMPStats{
			IncomingPackets:           uint64(info.stats.icmp_ipv4.incoming_packets),
			EchoResponses:             uint64(info.stats.icmp_ipv4.echo_responses),
			PayloadTooShortIP:         uint64(info.stats.icmp_ipv4.payload_too_short_ip),
			UnmatchingSrcFromOriginal: uint64(info.stats.icmp_ipv4.unmatching_src_from_original),
			PayloadTooShortPort:       uint64(info.stats.icmp_ipv4.payload_too_short_port),
			UnexpectedTransport:       uint64(info.stats.icmp_ipv4.unexpected_transport),
			UnrecognizedVS:            uint64(info.stats.icmp_ipv4.unrecognized_vs),
			ForwardedPackets:          uint64(info.stats.icmp_ipv4.forwarded_packets),
			BroadcastedPackets:        uint64(info.stats.icmp_ipv4.broadcasted_packets),
			PacketClonesSent:          uint64(info.stats.icmp_ipv4.packet_clones_sent),
			PacketClonesReceived:      uint64(info.stats.icmp_ipv4.packet_clones_received),
			PacketCloneFailures:       uint64(info.stats.icmp_ipv4.packet_clone_failures),
		},
		ICMPv6: module.ICMPStats{
			IncomingPackets:           uint64(info.stats.icmp_ipv6.incoming_packets),
			EchoResponses:             uint64(info.stats.icmp_ipv6.echo_responses),
			PayloadTooShortIP:         uint64(info.stats.icmp_ipv6.payload_too_short_ip),
			UnmatchingSrcFromOriginal: uint64(info.stats.icmp_ipv6.unmatching_src_from_original),
			PayloadTooShortPort:       uint64(info.stats.icmp_ipv6.payload_too_short_port),
			UnexpectedTransport:       uint64(info.stats.icmp_ipv6.unexpected_transport),
			UnrecognizedVS:            uint64(info.stats.icmp_ipv6.unrecognized_vs),
			ForwardedPackets:          uint64(info.stats.icmp_ipv6.forwarded_packets),
			BroadcastedPackets:        uint64(info.stats.icmp_ipv6.broadcasted_packets),
			PacketClonesSent:          uint64(info.stats.icmp_ipv6.packet_clones_sent),
			PacketClonesReceived:      uint64(info.stats.icmp_ipv6.packet_clones_received),
			PacketCloneFailures:       uint64(info.stats.icmp_ipv6.packet_clone_failures),
		},
		Common: module.CommonStats{
			IncomingPackets:        uint64(info.stats.common.incoming_packets),
			IncomingBytes:          uint64(info.stats.common.incoming_bytes),
			UnexpectedNetworkProto: uint64(info.stats.common.unexpected_network_proto),
			DecapSuccessful:        uint64(info.stats.common.decap_successful),
			DecapFailed:            uint64(info.stats.common.decap_failed),
			OutgoingPackets:        uint64(info.stats.common.outgoing_packets),
			OutgoingBytes:          uint64(info.stats.common.outgoing_bytes),
		},
	}

	// Fill virtual services info
	vsCount := int(info.virtual_services.count)
	if vsCount > 0 && info.virtual_services.info != nil {
		cArr := unsafe.Slice(
			(*C.struct_balancer_virtual_service_info)(info.virtual_services.info),
			vsCount,
		)
		result.VsInfo = make([]module.VsInfo, vsCount)
		for i := range vsCount {
			entry := cArr[i]
			addr := ipFromC(&entry.ip[0], entry.ip_proto)
			id := module.VsIdentifier{
				Ip:    addr,
				Port:  uint16(entry.virtual_port),
				Proto: moduleProtoFromC(entry.transport_proto),
			}
			stats := vsStatsFromC(&entry.stats)

			result.VsInfo[i] = module.VsInfo{
				VsRegistryIdx: uint(i),
				VsIdentifier:  id,
				LastPacketTimestamp: time.Unix(
					int64(entry.last_packet_timestamp),
					0,
				),
				Stats: stats,
			}
		}
	}

	// Fill reals info
	realCount := int(info.reals.count)
	if realCount > 0 && info.reals.info != nil {
		cArr := unsafe.Slice(
			(*C.struct_balancer_real_info)(info.reals.info),
			realCount,
		)
		result.RealInfo = make([]module.RealInfo, realCount)
		for i := range realCount {
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

			result.RealInfo[i] = module.RealInfo{
				RealRegistryIdx: uint(i),
				RealIdentifier:  realId,
				LastPacketTimestamp: time.Unix(
					int64(entry.last_packet_timestamp),
					0,
				),
				Stats: stats,
			}
		}
	}

	return result
}
