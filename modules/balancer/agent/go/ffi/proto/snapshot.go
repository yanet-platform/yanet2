package proto

// Direct conversions to protobuf

/*
#cgo CFLAGS: -I../../../ -I../../../../../../
#cgo LDFLAGS: -L../../../../../../build/modules/balancer/agent -lbalancer_agent
#cgo LDFLAGS: -L../../../../../../build/modules/balancer/controlplane/api -lbalancer_cp
#cgo LDFLAGS: -L../../../../../build/modules/balancer/controlplane/handler -lbalancer_packet_handler
#cgo LDFLAGS: -L../../../../../build/modules/balancer/controlplane/state -lbalancer_state
#include "agent.h"
#include "manager.h"
#include "modules/balancer/controlplane/api/graph.h"
#include "modules/balancer/controlplane/api/vs.h"
#include "modules/balancer/controlplane/api/real.h"
#include "modules/balancer/controlplane/api/inspect.h"
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
)

func ConvertBalancerSnapshot(csPtr unsafe.Pointer) *balancerpb.BalancerSnapshot {
	cs := (*C.struct_balancer_snapshot)(csPtr)
	if cs == nil {
		return nil
	}

	return &balancerpb.BalancerSnapshot{
		L4Stats:             convertL4Stats(&cs.l4_stats),
		CommonStats:         convertCommonStats(&cs.common_stats),
		IcmpIpv4Stats:       convertIcmpStats(&cs.icmp_ipv4_stats),
		IcmpIpv6Stats:       convertIcmpStats(&cs.icmp_ipv6_stats),
		ActiveSessions:      uint64(cs.active_sessions),
		LastPacketTimestamp: convertTimestamp(uint32(cs.last_packet_timestamp)),
		VirtualServices:     convertVsSnapshots(cs.vs_snapshots, cs.vs_count),
	}
}

// ============================================================
// VS Snapshots
// ============================================================

func convertVsSnapshots(
	ptr *C.struct_named_vs_snapshot,
	count C.size_t,
) []*balancerpb.VsSnapshot {
	n := int(count)
	if n == 0 || ptr == nil {
		return nil
	}

	// Zero-copy view into C array
	cSlice := unsafe.Slice(ptr, n)

	result := make([]*balancerpb.VsSnapshot, n)
	for i := range cSlice {
		result[i] = convertNamedVsSnapshot(&cSlice[i])
	}
	return result
}

func convertNamedVsSnapshot(c *C.struct_named_vs_snapshot) *balancerpb.VsSnapshot {
	if c == nil {
		return nil
	}
	vs := &c.snapshot

	return &balancerpb.VsSnapshot{
		Id:                  convertVsIdentifier(&c.vs_identifier),
		VsStats:             convertVsStats(&vs.stats),
		ActiveSessions:      uint64(vs.active_sessions),
		LastPacketTimestamp: convertTimestamp(uint32(vs.last_packet_timestamp)),
		AllowedSources:      convertAllowedSourcesStats(vs.allowed_sources, vs.allowed_sources_count),
		Reals:               convertRealSnapshots(vs.reals, vs.reals_count),
	}
}

// ============================================================
// Real Snapshots
// ============================================================

func convertRealSnapshots(
	ptr *C.struct_named_real_snapshot,
	count C.size_t,
) []*balancerpb.RealSnapshot {
	n := int(count)
	if n == 0 || ptr == nil {
		return nil
	}

	// Zero-copy view into C array
	cSlice := unsafe.Slice(ptr, n)

	result := make([]*balancerpb.RealSnapshot, n)
	for i := range cSlice {
		result[i] = convertNamedRealSnapshot(&cSlice[i])
	}
	return result
}

func convertNamedRealSnapshot(c *C.struct_named_real_snapshot) *balancerpb.RealSnapshot {
	if c == nil {
		return nil
	}
	r := &c.snapshot

	return &balancerpb.RealSnapshot{
		Id:                  convertRealIdentifier(&c.real_identifier),
		RealStats:           convertRealStats(&r.stats),
		ActiveSessions:      uint64(r.active_sessions),
		LastPacketTimestamp: convertTimestamp(uint32(r.last_packet_timestamp)),
		Weight:              uint64(r.weight),
		EffectiveWeight:     uint64(r.effective_weight),
		Enabled:             bool(r.enabled),
	}
}
