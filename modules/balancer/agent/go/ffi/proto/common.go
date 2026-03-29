package proto

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
	"google.golang.org/protobuf/types/known/timestamppb"
)

func convertNetAddr(cAddr *C.struct_net_addr, isV4 bool) *balancerpb.Addr {
	if isV4 {
		var v4 [4]byte
		pv4 := (*C.struct_net4_addr)(unsafe.Pointer(&cAddr))
		C.memcpy(unsafe.Pointer(&v4[0]), unsafe.Pointer(&pv4.bytes[0]), 4)
		return &balancerpb.Addr{
			Bytes: v4[:],
		}
	}
	var v6 [16]byte
	pv6 := (*C.struct_net6_addr)(unsafe.Pointer(&cAddr))
	C.memcpy(unsafe.Pointer(&v6[0]), unsafe.Pointer(&pv6.bytes[0]), 16)
	return &balancerpb.Addr{
		Bytes: v6[:],
	}
}

// ============================================================
// VS Identifier
//
// vs_identifier contains:
//   - struct net_addr addr  (IPv4 or IPv6 address)
//   - uint8_t ip_proto      (0=IPv4, 41=IPv6)
//   - uint16_t port
//   - uint8_t transport_proto (6=TCP, 17=UDP)
// ============================================================

func convertVsIdentifier(c *C.struct_vs_identifier) *balancerpb.VsIdentifier {
	if c == nil {
		return nil
	}
	return &balancerpb.VsIdentifier{
		Addr: convertNetAddr(&c.addr, c.ip_proto == C.uint8_t(0)),
		Port: uint32(c.port),
		Proto: func() balancerpb.TransportProto {
			if c.transport_proto == C.uint8_t(6) {
				return balancerpb.TransportProto_TCP
			} else {
				return balancerpb.TransportProto_UDP
			}
		}(),
	}
}

// ============================================================
// Real Identifier
//
// relative_real_identifier contains:
//   - struct net_addr addr
//   - uint16_t port
// ============================================================

func convertRealIdentifier(c *C.struct_relative_real_identifier) *balancerpb.RelativeRealIdentifier {
	if c == nil {
		return nil
	}
	return &balancerpb.RelativeRealIdentifier{
		Ip:   convertNetAddr(&c.addr, c.ip_proto == C.uint8_t(0)),
		Port: uint32(c.port),
	}
}

// ============================================================
// Timestamp
//
// C side stores uint32 monotonic seconds since boot.
// Proto Timestamp uses int64 seconds + int32 nanos.
// We only have second precision, nanos = 0.
// ============================================================

func convertTimestamp(unixSec uint32) *timestamppb.Timestamp {
	return &timestamppb.Timestamp{
		Seconds: int64(unixSec),
	}
}

func ConvertPacketHandlerRef(
	ref *balancerpb.PacketHandlerRef,
) *C.struct_packet_handler_ref {
	if ref == nil {
		return nil
	}

	cRef := (*C.struct_packet_handler_ref)(
		C.malloc(C.sizeof_struct_packet_handler_ref),
	)

	if ref.Device != nil {
		cRef.device = C.CString(*ref.Device)
	} else {
		cRef.device = nil
	}

	if ref.Pipeline != nil {
		cRef.pipeline = C.CString(*ref.Pipeline)
	} else {
		cRef.pipeline = nil
	}

	if ref.Function != nil {
		cRef.function = C.CString(*ref.Function)
	} else {
		cRef.function = nil
	}

	if ref.Chain != nil {
		cRef.chain = C.CString(*ref.Chain)
	} else {
		cRef.chain = nil
	}

	return cRef
}
