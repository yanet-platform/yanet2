package ffi

/*
#cgo CFLAGS: -I../../ -I../../../../../
#cgo LDFLAGS: -L../../../../../build/modules/balancer/agent -lbalancer_agent -L../../../../../build/modules/balancer/controlplane/api -lbalancer_cp -L../../../../../build/modules/balancer/controlplane/handler -lbalancer_packet_handler -L../../../../../build/modules/balancer/controlplane/state -lbalancer_state -lbalancer_packet_handler -lbalancer_state
#include "manager.h"
#include "modules/balancer/controlplane/api/graph.h"
#include "modules/balancer/controlplane/api/vs.h"
#include "modules/balancer/controlplane/api/real.h"
#include <stdlib.h>
#include <string.h>
*/
import "C"
import (
	"fmt"
	"net/netip"
	"time"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
)

// rangeToPrefixV4 converts an IPv4 address range to a CIDR prefix
func rangeToPrefixV4(from, to netip.Addr) netip.Prefix {
	fromBytes := from.As4()
	toBytes := to.As4()

	// Convert to uint32 for easier bit manipulation
	fromInt := uint32(
		fromBytes[0],
	)<<24 | uint32(
		fromBytes[1],
	)<<16 | uint32(
		fromBytes[2],
	)<<8 | uint32(
		fromBytes[3],
	)
	toInt := uint32(
		toBytes[0],
	)<<24 | uint32(
		toBytes[1],
	)<<16 | uint32(
		toBytes[2],
	)<<8 | uint32(
		toBytes[3],
	)

	// XOR to find differing bits
	diff := fromInt ^ toInt

	// Count leading zeros to get prefix length
	bits := 32
	if diff != 0 {
		// Find the position of the highest set bit
		for i := 31; i >= 0; i-- {
			if (diff & (1 << i)) != 0 {
				bits = 31 - i
				break
			}
		}
	}

	return netip.PrefixFrom(from, bits)
}

// rangeToPrefixV6 converts an IPv6 address range to a CIDR prefix
func rangeToPrefixV6(from, to netip.Addr) netip.Prefix {
	fromBytes := from.As16()
	toBytes := to.As16()

	// Find first differing byte
	bits := 0
	for i := 0; i < 16; i++ {
		if fromBytes[i] != toBytes[i] {
			// XOR to find differing bits in this byte
			diff := fromBytes[i] ^ toBytes[i]
			// Count leading zeros in this byte
			for j := 7; j >= 0; j-- {
				if (diff & (1 << j)) != 0 {
					bits += 7 - j
					return netip.PrefixFrom(from, bits)
				}
			}
			bits += 8
		} else {
			bits += 8
		}
	}

	return netip.PrefixFrom(from, bits)
}

func goToC_NetAddr(addr netip.Addr) C.struct_net_addr {
	var cAddr C.struct_net_addr
	// Zero-initialize the entire union to avoid padding issues
	ptr := unsafe.Pointer(&cAddr)
	size := unsafe.Sizeof(cAddr)
	slice := unsafe.Slice((*byte)(ptr), size)
	for i := range slice {
		slice[i] = 0
	}

	if addr.Is4() {
		v4 := addr.As4()
		// Access union field through unsafe pointer cast
		pv4 := (*C.struct_net4_addr)(unsafe.Pointer(&cAddr))
		C.memcpy(unsafe.Pointer(&pv4.bytes[0]), unsafe.Pointer(&v4[0]), 4)
	} else {
		v6 := addr.As16()
		// Access union field through unsafe pointer cast
		pv6 := (*C.struct_net6_addr)(unsafe.Pointer(&cAddr))
		C.memcpy(unsafe.Pointer(&pv6.bytes[0]), unsafe.Pointer(&v6[0]), 16)
	}
	return cAddr
}

func cToGo_NetAddr(cAddr C.struct_net_addr, isV4 bool) netip.Addr {
	if isV4 {
		var v4 [4]byte
		pv4 := (*C.struct_net4_addr)(unsafe.Pointer(&cAddr))
		C.memcpy(unsafe.Pointer(&v4[0]), unsafe.Pointer(&pv4.bytes[0]), 4)
		return netip.AddrFrom4(v4)
	}
	var v6 [16]byte
	pv6 := (*C.struct_net6_addr)(unsafe.Pointer(&cAddr))
	C.memcpy(unsafe.Pointer(&v6[0]), unsafe.Pointer(&pv6.bytes[0]), 16)
	return netip.AddrFrom16(v6)
}

func goToC_Net(net xnetip.NetWithMask) C.struct_net {
	var cNet C.struct_net
	addr := net.Addr
	mask := net.MaskBytes()

	// Zero-initialize the entire union to avoid garbage data
	ptr := unsafe.Pointer(&cNet)
	size := unsafe.Sizeof(cNet)
	slice := unsafe.Slice((*byte)(ptr), size)
	for i := range slice {
		slice[i] = 0
	}

	if addr.Is4() {
		v4 := addr.As4()
		// For IPv4, the struct net4 layout is:
		// - addr[4] at offset 0
		// - mask[4] at offset 4
		// Copy addr to bytes 0-3
		for i := 0; i < 4; i++ {
			slice[i] = v4[i]
		}
		// Copy mask to bytes 4-7
		for i := 0; i < 4; i++ {
			slice[4+i] = mask[i]
		}
	} else {
		v6 := addr.As16()
		// For IPv6, the struct net6 layout is:
		// - addr[16] at offset 0
		// - mask[16] at offset 16
		// Copy addr to bytes 0-15
		for i := 0; i < 16; i++ {
			slice[i] = v6[i]
		}
		// Copy mask to bytes 16-31
		for i := 0; i < 16; i++ {
			slice[16+i] = mask[i]
		}
	}
	return cNet
}

func cToGo_Net(cNet C.struct_net, isV4 bool) xnetip.NetWithMask {
	if isV4 {
		var addr [4]byte
		var mask [4]byte
		// Copy from union bytes
		C.memcpy(unsafe.Pointer(&addr[0]), unsafe.Pointer(&cNet), 4)
		C.memcpy(
			unsafe.Pointer(&mask[0]),
			unsafe.Pointer(uintptr(unsafe.Pointer(&cNet))+4),
			4,
		)

		return xnetip.NetWithMask{
			Addr: netip.AddrFrom4(addr),
			Mask: mask[:],
		}
	}

	var addr [16]byte
	var mask [16]byte
	// Copy from union bytes
	C.memcpy(unsafe.Pointer(&addr[0]), unsafe.Pointer(&cNet), 16)
	C.memcpy(
		unsafe.Pointer(&mask[0]),
		unsafe.Pointer(uintptr(unsafe.Pointer(&cNet))+16),
		16,
	)

	return xnetip.NetWithMask{
		Addr: netip.AddrFrom16(addr),
		Mask: mask[:],
	}
}

// VS type conversions

func goToC_VsIdentifier(id VsIdentifier) C.struct_vs_identifier {
	var cId C.struct_vs_identifier
	// Zero-initialize the entire structure to avoid padding issues
	ptr := unsafe.Pointer(&cId)
	size := unsafe.Sizeof(cId)
	slice := unsafe.Slice((*byte)(ptr), size)
	for i := range slice {
		slice[i] = 0
	}

	cId.addr = goToC_NetAddr(id.Addr)
	// Derive ip_proto from the address type
	if id.Addr.Is4() {
		cId.ip_proto = 0 // IPPROTO_IP (IPv4)
	} else {
		cId.ip_proto = 41 // IPPROTO_IPV6
	}
	cId.port = C.uint16_t(id.Port)
	// Convert Go enum (0=TCP, 1=UDP) to C constants (6=IPPROTO_TCP, 17=IPPROTO_UDP)
	if id.TransportProto == VsTransportProtoTcp {
		cId.transport_proto = C.IPPROTO_TCP // 6
	} else {
		cId.transport_proto = C.IPPROTO_UDP // 17
	}
	return cId
}

func cToGo_VsIdentifier(cId C.struct_vs_identifier) VsIdentifier {
	// Determine if IPv4 or IPv6 based on ip_proto
	isV4 := cId.ip_proto == 0 // IPPROTO_IP (IPv4)
	return VsIdentifier{
		Addr: cToGo_NetAddr(cId.addr, isV4),
		Port: uint16(cId.port),
		// Convert C constants (6=IPPROTO_TCP, 17=IPPROTO_UDP) to Go enum (0=TCP, 1=UDP)
		TransportProto: func() VsTransportProto {
			if cId.transport_proto == C.IPPROTO_TCP { // 6
				return VsTransportProtoTcp // 0
			} else {
				return VsTransportProtoUdp // 1
			}
		}(),
	}
}

// Real type conversions

func goToC_RelativeRealIdentifier(
	id RelativeRealIdentifier,
) C.struct_relative_real_identifier {
	var cId C.struct_relative_real_identifier
	// Zero-initialize the entire structure to avoid padding issues
	ptr := unsafe.Pointer(&cId)
	size := unsafe.Sizeof(cId)
	slice := unsafe.Slice((*byte)(ptr), size)
	for i := range slice {
		slice[i] = 0
	}

	cId.addr = goToC_NetAddr(id.Addr)
	// Derive ip_proto from the address type
	if id.Addr.Is4() {
		cId.ip_proto = 0 // IPPROTO_IP (IPv4)
	} else {
		cId.ip_proto = 41 // IPPROTO_IPV6
	}
	cId.port = C.uint16_t(id.Port)
	return cId
}

func cToGo_RelativeRealIdentifier(
	cId C.struct_relative_real_identifier,
) RelativeRealIdentifier {
	isV4 := cId.ip_proto == 0 // IPPROTO_IP (IPv4)
	return RelativeRealIdentifier{
		Addr: cToGo_NetAddr(cId.addr, isV4),
		Port: uint16(cId.port),
	}
}

func goToC_RealIdentifier(id RealIdentifier) C.struct_real_identifier {
	var cId C.struct_real_identifier
	// Zero-initialize the entire structure to avoid padding issues
	ptr := unsafe.Pointer(&cId)
	size := unsafe.Sizeof(cId)
	slice := unsafe.Slice((*byte)(ptr), size)
	for i := range slice {
		slice[i] = 0
	}

	cId.vs_identifier = goToC_VsIdentifier(id.VsIdentifier)
	cId.relative = goToC_RelativeRealIdentifier(id.Relative)
	return cId
}

func cToGo_RealIdentifier(cId C.struct_real_identifier) RealIdentifier {
	return RealIdentifier{
		VsIdentifier: cToGo_VsIdentifier(cId.vs_identifier),
		Relative:     cToGo_RelativeRealIdentifier(cId.relative),
	}
}

// Time conversions (uint32 monotonic timestamp to time.Time)
func cToGo_Timestamp(ts uint32) time.Time {
	return time.Unix(int64(ts), 0)
}

func goToC_Timestamp(t time.Time) uint32 {
	return uint32(t.Unix())
}

// RealUpdate conversions

func goToC_RealUpdate(update RealUpdate) C.struct_real_update {
	var cUpdate C.struct_real_update
	cUpdate.identifier = goToC_RealIdentifier(update.Identifier)
	cUpdate.weight = C.uint16_t(update.Weight)
	cUpdate.enabled = C.uint8_t(update.Enabled)
	return cUpdate
}

// PacketHandlerRef conversions

func goToC_PacketHandlerRef(
	ref *PacketHandlerRef,
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

func freeC_PacketHandlerRef(cRef *C.struct_packet_handler_ref) {
	if cRef == nil {
		return
	}

	if cRef.device != nil {
		C.free(unsafe.Pointer(cRef.device))
	}
	if cRef.pipeline != nil {
		C.free(unsafe.Pointer(cRef.pipeline))
	}
	if cRef.function != nil {
		C.free(unsafe.Pointer(cRef.function))
	}
	if cRef.chain != nil {
		C.free(unsafe.Pointer(cRef.chain))
	}

	C.free(unsafe.Pointer(cRef))
}

// BalancerManagerConfig conversions

func goToC_BalancerManagerConfig(
	config *BalancerManagerConfig,
) (*C.struct_balancer_manager_config, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	cConfig := (*C.struct_balancer_manager_config)(
		C.malloc(C.sizeof_struct_balancer_manager_config),
	)

	// Convert balancer config directly into the embedded struct
	err := goToC_BalancerConfigInPlace(&config.Balancer, &cConfig.balancer)
	if err != nil {
		C.free(unsafe.Pointer(cConfig))
		return nil, err
	}

	// Convert WLC config
	cConfig.wlc.power = C.size_t(config.Wlc.Power)
	cConfig.wlc.max_real_weight = C.size_t(config.Wlc.MaxRealWeight)
	cConfig.wlc.vs_count = C.size_t(len(config.Wlc.Vs))

	if len(config.Wlc.Vs) > 0 {
		cConfig.wlc.vs = (*C.uint32_t)(
			C.malloc(C.size_t(len(config.Wlc.Vs)) * C.sizeof_uint32_t),
		)
		cVsSlice := unsafe.Slice(cConfig.wlc.vs, len(config.Wlc.Vs))
		for i, vs := range config.Wlc.Vs {
			cVsSlice[i] = C.uint32_t(vs)
		}
	} else {
		cConfig.wlc.vs = nil
	}

	cConfig.refresh_period = C.uint32_t(config.RefreshPeriod.Milliseconds())
	cConfig.max_load_factor = C.float(config.MaxLoadFactor)

	return cConfig, nil
}

func freeC_BalancerManagerConfig(cConfig *C.struct_balancer_manager_config) {
	if cConfig == nil {
		return
	}

	// Free balancer config internals
	freeC_BalancerConfig(&cConfig.balancer)

	// Free WLC VS array
	if cConfig.wlc.vs != nil {
		C.free(unsafe.Pointer(cConfig.wlc.vs))
	}

	C.free(unsafe.Pointer(cConfig))
}

func cToGo_BalancerManagerConfig(
	cConfig *C.struct_balancer_manager_config,
) *BalancerManagerConfig {
	if cConfig == nil {
		return nil
	}

	config := &BalancerManagerConfig{
		Balancer:      *cToGo_BalancerConfig(&cConfig.balancer),
		RefreshPeriod: time.Duration(cConfig.refresh_period) * time.Millisecond,
		MaxLoadFactor: float32(cConfig.max_load_factor),
	}

	// Convert WLC config
	config.Wlc.Power = uint(cConfig.wlc.power)
	config.Wlc.MaxRealWeight = uint(cConfig.wlc.max_real_weight)

	if cConfig.wlc.vs_count > 0 && cConfig.wlc.vs != nil {
		cVsSlice := unsafe.Slice(cConfig.wlc.vs, cConfig.wlc.vs_count)
		config.Wlc.Vs = make([]uint32, cConfig.wlc.vs_count)
		for i := range config.Wlc.Vs {
			config.Wlc.Vs[i] = uint32(cVsSlice[i])
		}
	} else {
		config.Wlc.Vs = []uint32{}
	}

	return config
}

// BalancerConfig conversions

func goToC_BalancerConfig(
	config *BalancerConfig,
) (*C.struct_balancer_config, error) {
	cConfig := (*C.struct_balancer_config)(
		C.malloc(C.sizeof_struct_balancer_config),
	)
	err := goToC_BalancerConfigInPlace(config, cConfig)
	if err != nil {
		C.free(unsafe.Pointer(cConfig))
		return nil, err
	}
	return cConfig, nil
}

func goToC_BalancerConfigInPlace(
	config *BalancerConfig,
	cConfig *C.struct_balancer_config,
) error {
	// Convert handler config directly into the embedded struct
	err := goToC_PacketHandlerConfigInPlace(&config.Handler, &cConfig.handler)
	if err != nil {
		return err
	}

	// Convert state config
	cConfig.state.table_capacity = C.size_t(config.State.TableCapacity)

	return nil
}

func freeC_BalancerConfig(cConfig *C.struct_balancer_config) {
	if cConfig == nil {
		return
	}

	freeC_PacketHandlerConfig(&cConfig.handler)
}

func cToGo_BalancerConfig(cConfig *C.struct_balancer_config) *BalancerConfig {
	if cConfig == nil {
		return nil
	}

	return &BalancerConfig{
		Handler: *cToGo_PacketHandlerConfig(&cConfig.handler),
		State: StateConfig{
			TableCapacity: uint(cConfig.state.table_capacity),
		},
	}
}

// PacketHandlerConfig conversions

func goToC_PacketHandlerConfig(
	config *PacketHandlerConfig,
) (*C.struct_packet_handler_config, error) {
	cConfig := (*C.struct_packet_handler_config)(
		C.malloc(C.sizeof_struct_packet_handler_config),
	)
	err := goToC_PacketHandlerConfigInPlace(config, cConfig)
	if err != nil {
		C.free(unsafe.Pointer(cConfig))
		return nil, err
	}
	return cConfig, nil
}

func goToC_PacketHandlerConfigInPlace(
	config *PacketHandlerConfig,
	cConfig *C.struct_packet_handler_config,
) error {
	if config == nil {
		return fmt.Errorf("config is nil")
	}
	if len(config.SourceV4.AsSlice()) == 0 {
		return fmt.Errorf("IPv4 source address is empty")
	}
	if len(config.SourceV6.AsSlice()) == 0 {
		return fmt.Errorf("IPv6 source address is empty")
	}

	// Convert sessions timeouts
	cConfig.sessions_timeouts.tcp_syn_ack = C.uint32_t(
		config.SessionsTimeouts.TcpSynAck,
	)
	cConfig.sessions_timeouts.tcp_syn = C.uint32_t(
		config.SessionsTimeouts.TcpSyn,
	)
	cConfig.sessions_timeouts.tcp_fin = C.uint32_t(
		config.SessionsTimeouts.TcpFin,
	)
	cConfig.sessions_timeouts.tcp = C.uint32_t(config.SessionsTimeouts.Tcp)
	cConfig.sessions_timeouts.udp = C.uint32_t(config.SessionsTimeouts.Udp)
	cConfig.sessions_timeouts.def = C.uint32_t(config.SessionsTimeouts.Default)

	// Convert source addresses (need to cast from net_addr to net4_addr/net6_addr)
	v4 := config.SourceV4.As4()
	C.memcpy(
		unsafe.Pointer(&cConfig.source_v4.bytes[0]),
		unsafe.Pointer(&v4[0]),
		4,
	)

	v6 := config.SourceV6.As16()
	C.memcpy(
		unsafe.Pointer(&cConfig.source_v6.bytes[0]),
		unsafe.Pointer(&v6[0]),
		16,
	)

	// Convert decap addresses
	cConfig.decap_v4_count = C.size_t(len(config.DecapV4))
	cConfig.decap_v6_count = C.size_t(len(config.DecapV6))

	if len(config.DecapV4) > 0 {
		cConfig.decap_v4 = (*C.struct_net4_addr)(
			C.malloc(C.size_t(len(config.DecapV4)) * C.sizeof_struct_net4_addr),
		)
		cDecapV4Slice := unsafe.Slice(cConfig.decap_v4, len(config.DecapV4))
		for i, addr := range config.DecapV4 {
			v4 := addr.As4()
			C.memcpy(
				unsafe.Pointer(&cDecapV4Slice[i].bytes[0]),
				unsafe.Pointer(&v4[0]),
				4,
			)
		}
	} else {
		cConfig.decap_v4 = nil
	}

	if len(config.DecapV6) > 0 {
		cConfig.decap_v6 = (*C.struct_net6_addr)(
			C.malloc(C.size_t(len(config.DecapV6)) * C.sizeof_struct_net6_addr),
		)
		cDecapV6Slice := unsafe.Slice(cConfig.decap_v6, len(config.DecapV6))
		for i, addr := range config.DecapV6 {
			v6 := addr.As16()
			C.memcpy(
				unsafe.Pointer(&cDecapV6Slice[i].bytes[0]),
				unsafe.Pointer(&v6[0]),
				16,
			)
		}
	} else {
		cConfig.decap_v6 = nil
	}

	// Convert virtual services
	cConfig.vs_count = C.size_t(len(config.VirtualServices))
	if len(config.VirtualServices) > 0 {
		cConfig.vs = (*C.struct_named_vs_config)(
			C.malloc(
				C.size_t(
					len(config.VirtualServices),
				) * C.sizeof_struct_named_vs_config,
			),
		)
		cVsSlice := unsafe.Slice(cConfig.vs, len(config.VirtualServices))
		for i, vs := range config.VirtualServices {
			err := goToC_VsConfigInPlace(&vs, &cVsSlice[i])
			if err != nil {
				// Cleanup on error
				freeC_PacketHandlerConfig(cConfig)
				return err
			}
		}
	} else {
		cConfig.vs = nil
	}

	return nil
}

func freeC_PacketHandlerConfig(cConfig *C.struct_packet_handler_config) {
	if cConfig == nil {
		return
	}

	if cConfig.decap_v4 != nil {
		C.free(unsafe.Pointer(cConfig.decap_v4))
	}
	if cConfig.decap_v6 != nil {
		C.free(unsafe.Pointer(cConfig.decap_v6))
	}

	if cConfig.vs != nil {
		cVsSlice := unsafe.Slice(cConfig.vs, cConfig.vs_count)
		for i := range cVsSlice {
			// Free only the internal allocations, not the struct itself
			// since it's part of an array
			if cVsSlice[i].config.reals != nil {
				C.free(unsafe.Pointer(cVsSlice[i].config.reals))
			}
			if cVsSlice[i].config.allowed_src != nil {
				C.free(unsafe.Pointer(cVsSlice[i].config.allowed_src))
			}
			if cVsSlice[i].config.peers_v4 != nil {
				C.free(unsafe.Pointer(cVsSlice[i].config.peers_v4))
			}
			if cVsSlice[i].config.peers_v6 != nil {
				C.free(unsafe.Pointer(cVsSlice[i].config.peers_v6))
			}
		}
		C.free(unsafe.Pointer(cConfig.vs))
	}
}

func cToGo_PacketHandlerConfig(
	cConfig *C.struct_packet_handler_config,
) *PacketHandlerConfig {
	if cConfig == nil {
		return nil
	}

	config := &PacketHandlerConfig{
		SessionsTimeouts: SessionsTimeouts{
			TcpSynAck: uint32(cConfig.sessions_timeouts.tcp_syn_ack),
			TcpSyn:    uint32(cConfig.sessions_timeouts.tcp_syn),
			TcpFin:    uint32(cConfig.sessions_timeouts.tcp_fin),
			Tcp:       uint32(cConfig.sessions_timeouts.tcp),
			Udp:       uint32(cConfig.sessions_timeouts.udp),
			Default:   uint32(cConfig.sessions_timeouts.def),
		},
		SourceV4: func() netip.Addr {
			var v4 [4]byte
			C.memcpy(
				unsafe.Pointer(&v4[0]),
				unsafe.Pointer(&cConfig.source_v4.bytes[0]),
				4,
			)
			return netip.AddrFrom4(v4)
		}(),
		SourceV6: func() netip.Addr {
			var v6 [16]byte
			C.memcpy(
				unsafe.Pointer(&v6[0]),
				unsafe.Pointer(&cConfig.source_v6.bytes[0]),
				16,
			)
			return netip.AddrFrom16(v6)
		}(),
	}

	// Convert decap addresses
	if cConfig.decap_v4_count > 0 && cConfig.decap_v4 != nil {
		cDecapV4Slice := unsafe.Slice(cConfig.decap_v4, cConfig.decap_v4_count)
		config.DecapV4 = make([]netip.Addr, cConfig.decap_v4_count)
		for i := range config.DecapV4 {
			var v4 [4]byte
			C.memcpy(
				unsafe.Pointer(&v4[0]),
				unsafe.Pointer(&cDecapV4Slice[i].bytes[0]),
				4,
			)
			config.DecapV4[i] = netip.AddrFrom4(v4)
		}
	} else {
		config.DecapV4 = []netip.Addr{}
	}

	if cConfig.decap_v6_count > 0 && cConfig.decap_v6 != nil {
		cDecapV6Slice := unsafe.Slice(cConfig.decap_v6, cConfig.decap_v6_count)
		config.DecapV6 = make([]netip.Addr, cConfig.decap_v6_count)
		for i := range config.DecapV6 {
			var v6 [16]byte
			C.memcpy(
				unsafe.Pointer(&v6[0]),
				unsafe.Pointer(&cDecapV6Slice[i].bytes[0]),
				16,
			)
			config.DecapV6[i] = netip.AddrFrom16(v6)
		}
	} else {
		config.DecapV6 = []netip.Addr{}
	}

	// Convert virtual services
	if cConfig.vs_count > 0 && cConfig.vs != nil {
		cVsSlice := unsafe.Slice(cConfig.vs, cConfig.vs_count)
		config.VirtualServices = make([]VsConfig, cConfig.vs_count)
		for i := range config.VirtualServices {
			config.VirtualServices[i] = *cToGo_VsConfig(&cVsSlice[i])
		}
	} else {
		config.VirtualServices = []VsConfig{}
	}

	return config
}

// VsConfig conversions

func goToC_VsConfig(config *VsConfig) (*C.struct_named_vs_config, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	cConfig := (*C.struct_named_vs_config)(
		C.malloc(C.sizeof_struct_named_vs_config),
	)
	err := goToC_VsConfigInPlace(config, cConfig)
	if err != nil {
		C.free(unsafe.Pointer(cConfig))
		return nil, err
	}
	return cConfig, nil
}

func goToC_VsConfigInPlace(
	config *VsConfig,
	cConfig *C.struct_named_vs_config,
) error {
	if config == nil {
		return fmt.Errorf("config is nil")
	}

	// Convert identifier
	cConfig.identifier = goToC_VsIdentifier(config.Identifier)

	// Convert flags to C bitfield (using constants from vs.h)
	var flags C.uint8_t
	if config.Flags.PureL3 {
		flags |= C.VS_PURE_L3_FLAG
	}
	if config.Flags.FixMSS {
		flags |= C.VS_FIX_MSS_FLAG
	}
	if config.Flags.GRE {
		flags |= C.VS_GRE_FLAG
	}
	if config.Flags.OPS {
		flags |= C.VS_OPS_FLAG
	}
	cConfig.config.flags = flags

	// Convert scheduler (enum vs_scheduler from vs.h: source_hash=0, round_robin=1)
	// Cast through int to match C enum type
	cConfig.config.scheduler = C.enum_vs_scheduler(C.int(config.Scheduler))

	// Convert reals array
	cConfig.config.real_count = C.size_t(len(config.Reals))
	if len(config.Reals) > 0 {
		cConfig.config.reals = (*C.struct_named_real_config)(
			C.malloc(
				C.size_t(
					len(config.Reals),
				) * C.size_t(
					unsafe.Sizeof(C.struct_named_real_config{}),
				),
			),
		)
		cRealsSlice := unsafe.Slice(cConfig.config.reals, len(config.Reals))
		for i, real := range config.Reals {
			cRealsSlice[i].real = goToC_RelativeRealIdentifier(real.Identifier)
			cRealsSlice[i].config.src = goToC_Net(real.Src)
			cRealsSlice[i].config.weight = C.uint16_t(real.Weight)
		}
	} else {
		cConfig.config.reals = nil
	}

	// Convert allowed sources
	cConfig.config.allowed_src_count = C.size_t(len(config.AllowedSrc))
	if len(config.AllowedSrc) > 0 {
		cConfig.config.allowed_src = (*C.struct_net_addr_range)(
			C.malloc(
				C.size_t(
					len(config.AllowedSrc),
				) * C.size_t(
					unsafe.Sizeof(C.struct_net_addr_range{}),
				),
			),
		)
		cAllowedSlice := unsafe.Slice(
			cConfig.config.allowed_src,
			len(config.AllowedSrc),
		)
		for i, prefix := range config.AllowedSrc {
			cAllowedSlice[i].from = goToC_NetAddr(prefix.Addr())
			cAllowedSlice[i].to = goToC_NetAddr(xnetip.LastAddr(prefix))
		}
	} else {
		cConfig.config.allowed_src = nil
	}

	// Convert IPv4 peers
	cConfig.config.peers_v4_count = C.size_t(len(config.PeersV4))
	if len(config.PeersV4) > 0 {
		cConfig.config.peers_v4 = (*C.struct_net4_addr)(
			C.malloc(C.size_t(len(config.PeersV4)) * C.sizeof_struct_net4_addr),
		)
		cPeersV4Slice := unsafe.Slice(
			cConfig.config.peers_v4,
			len(config.PeersV4),
		)
		for i, peer := range config.PeersV4 {
			v4 := peer.As4()
			C.memcpy(
				unsafe.Pointer(&cPeersV4Slice[i].bytes[0]),
				unsafe.Pointer(&v4[0]),
				4,
			)
		}
	} else {
		cConfig.config.peers_v4 = nil
	}

	// Convert IPv6 peers
	cConfig.config.peers_v6_count = C.size_t(len(config.PeersV6))
	if len(config.PeersV6) > 0 {
		cConfig.config.peers_v6 = (*C.struct_net6_addr)(
			C.malloc(C.size_t(len(config.PeersV6)) * C.sizeof_struct_net6_addr),
		)
		cPeersV6Slice := unsafe.Slice(
			cConfig.config.peers_v6,
			len(config.PeersV6),
		)
		for i, peer := range config.PeersV6 {
			v6 := peer.As16()
			C.memcpy(
				unsafe.Pointer(&cPeersV6Slice[i].bytes[0]),
				unsafe.Pointer(&v6[0]),
				16,
			)
		}
	} else {
		cConfig.config.peers_v6 = nil
	}

	return nil
}

func freeC_VsConfig(cConfig *C.struct_named_vs_config) {
	if cConfig == nil {
		return
	}

	if cConfig.config.reals != nil {
		C.free(unsafe.Pointer(cConfig.config.reals))
	}
	if cConfig.config.allowed_src != nil {
		C.free(unsafe.Pointer(cConfig.config.allowed_src))
	}
	if cConfig.config.peers_v4 != nil {
		C.free(unsafe.Pointer(cConfig.config.peers_v4))
	}
	if cConfig.config.peers_v6 != nil {
		C.free(unsafe.Pointer(cConfig.config.peers_v6))
	}
}

func cToGo_VsConfig(cConfig *C.struct_named_vs_config) *VsConfig {
	if cConfig == nil {
		return nil
	}

	config := &VsConfig{
		Identifier: cToGo_VsIdentifier(cConfig.identifier),
		Scheduler:  VsScheduler(cConfig.config.scheduler),
	}

	// Convert flags from C bitfield
	config.Flags.PureL3 = (cConfig.config.flags & C.VS_PURE_L3_FLAG) != 0
	config.Flags.FixMSS = (cConfig.config.flags & C.VS_FIX_MSS_FLAG) != 0
	config.Flags.GRE = (cConfig.config.flags & C.VS_GRE_FLAG) != 0
	config.Flags.OPS = (cConfig.config.flags & C.VS_OPS_FLAG) != 0

	// Convert reals array
	if cConfig.config.real_count > 0 && cConfig.config.reals != nil {
		cRealsSlice := unsafe.Slice(
			cConfig.config.reals,
			cConfig.config.real_count,
		)
		config.Reals = make([]RealConfig, cConfig.config.real_count)
		for i := range config.Reals {
			relative := cToGo_RelativeRealIdentifier(cRealsSlice[i].real)
			isV4 := relative.Addr.Is4()
			config.Reals[i] = RealConfig{
				Identifier: relative,
				Src:        cToGo_Net(cRealsSlice[i].config.src, isV4),
				Weight:     uint16(cRealsSlice[i].config.weight),
			}
		}
	} else {
		config.Reals = []RealConfig{}
	}

	// Convert allowed sources
	if cConfig.config.allowed_src_count > 0 &&
		cConfig.config.allowed_src != nil {
		cAllowedSlice := unsafe.Slice(
			cConfig.config.allowed_src,
			cConfig.config.allowed_src_count,
		)
		config.AllowedSrc = make(
			[]netip.Prefix,
			cConfig.config.allowed_src_count,
		)
		for i := range config.AllowedSrc {
			// Determine if IPv4 or IPv6 from the VS identifier address
			isV4 := config.Identifier.Addr.Is4()
			from := cToGo_NetAddr(cAllowedSlice[i].from, isV4)
			to := cToGo_NetAddr(cAllowedSlice[i].to, isV4)

			// Convert range to prefix
			if isV4 {
				config.AllowedSrc[i] = rangeToPrefixV4(from, to)
			} else {
				config.AllowedSrc[i] = rangeToPrefixV6(from, to)
			}
		}
	} else {
		config.AllowedSrc = []netip.Prefix{}
	}

	// Convert IPv4 peers
	if cConfig.config.peers_v4_count > 0 && cConfig.config.peers_v4 != nil {
		cPeersV4Slice := unsafe.Slice(
			cConfig.config.peers_v4,
			cConfig.config.peers_v4_count,
		)
		config.PeersV4 = make([]netip.Addr, cConfig.config.peers_v4_count)
		for i := range config.PeersV4 {
			var v4 [4]byte
			C.memcpy(
				unsafe.Pointer(&v4[0]),
				unsafe.Pointer(&cPeersV4Slice[i].bytes[0]),
				4,
			)
			config.PeersV4[i] = netip.AddrFrom4(v4)
		}
	} else {
		config.PeersV4 = []netip.Addr{}
	}

	// Convert IPv6 peers
	if cConfig.config.peers_v6_count > 0 && cConfig.config.peers_v6 != nil {
		cPeersV6Slice := unsafe.Slice(
			cConfig.config.peers_v6,
			cConfig.config.peers_v6_count,
		)
		config.PeersV6 = make([]netip.Addr, cConfig.config.peers_v6_count)
		for i := range config.PeersV6 {
			var v6 [16]byte
			C.memcpy(
				unsafe.Pointer(&v6[0]),
				unsafe.Pointer(&cPeersV6Slice[i].bytes[0]),
				16,
			)
			config.PeersV6[i] = netip.AddrFrom16(v6)
		}
	} else {
		config.PeersV6 = []netip.Addr{}
	}

	return config
}

// BalancerInfo conversions

func cToGo_BalancerInfo(cInfo *C.struct_balancer_info) *BalancerInfo {
	info := &BalancerInfo{
		ActiveSessions:      uint64(cInfo.active_sessions),
		LastPacketTimestamp: time.Unix(int64(cInfo.last_packet_timestamp), 0),
	}

	// Convert VS info array
	if cInfo.vs_count > 0 && cInfo.vs != nil {
		cVsSlice := unsafe.Slice(cInfo.vs, cInfo.vs_count)
		info.Vs = make([]VsInfo, cInfo.vs_count)
		for i := range info.Vs {
			info.Vs[i] = *cToGo_VsInfo(&cVsSlice[i])
		}
	}

	return info
}

func cToGo_VsInfo(cInfo *C.struct_named_vs_info) *VsInfo {
	info := &VsInfo{
		Identifier:          cToGo_VsIdentifier(cInfo.identifier),
		LastPacketTimestamp: time.Unix(int64(cInfo.last_packet_timestamp), 0),
		ActiveSessions:      uint64(cInfo.active_sessions),
	}

	// Convert reals array
	if cInfo.reals_count > 0 && cInfo.reals != nil {
		cRealsSlice := unsafe.Slice(cInfo.reals, cInfo.reals_count)
		info.Reals = make([]RealInfo, cInfo.reals_count)
		for i := range info.Reals {
			relative := cToGo_RelativeRealIdentifier(cRealsSlice[i].real)
			info.Reals[i] = RealInfo{
				Dst: relative.Addr,
				LastPacketTimestamp: time.Unix(
					int64(cRealsSlice[i].last_packet_timestamp),
					0,
				),
				ActiveSessions: uint64(cRealsSlice[i].active_sessions),
			}
		}
	}

	return info
}

// Sessions conversions

func cToGo_Sessions(cSessions *C.struct_sessions) *Sessions {
	if cSessions == nil {
		return nil
	}

	sessions := &Sessions{}

	// Convert sessions array
	if cSessions.sessions_count > 0 && cSessions.sessions != nil {
		cSessionsSlice := unsafe.Slice(
			cSessions.sessions,
			cSessions.sessions_count,
		)
		sessions.Sessions = make([]struct {
			Identifier SessionIdentifier
			Info       SessionInfo
		}, cSessions.sessions_count)

		for i := range sessions.Sessions {
			sessions.Sessions[i].Identifier = cToGo_SessionIdentifier(
				&cSessionsSlice[i].identifier,
			)
			sessions.Sessions[i].Info = cToGo_SessionInfo(
				&cSessionsSlice[i].info,
			)
		}
	}

	return sessions
}

func cToGo_SessionIdentifier(
	cId *C.struct_session_identifier,
) SessionIdentifier {
	real := cToGo_RealIdentifier(cId.real)
	return SessionIdentifier{
		ClientIp: cToGo_NetAddr(
			cId.client_ip,
			real.VsIdentifier.Addr.Is4(),
		),
		ClientPort: uint16(cId.client_port),
		Real:       real,
	}
}

func cToGo_SessionInfo(cInfo *C.struct_session_info) SessionInfo {
	return SessionInfo{
		CreateTimestamp:     time.Unix(int64(cInfo.create_timestamp), 0),
		LastPacketTimestamp: time.Unix(int64(cInfo.last_packet_timestamp), 0),
		Timeout:             time.Duration(cInfo.timeout) * time.Second,
	}
}

// BalancerStats conversions

func cToGo_BalancerStats(cStats *C.struct_balancer_stats) *BalancerStats {
	if cStats == nil {
		return nil
	}

	stats := &BalancerStats{
		L4:       cToGo_L4Stats(&cStats.l4),
		IcmpIpv4: cToGo_IcmpStats(&cStats.icmp_ipv4),
		IcmpIpv6: cToGo_IcmpStats(&cStats.icmp_ipv6),
		Common:   cToGo_CommonStats(&cStats.common),
	}

	// Convert VS stats array
	if cStats.vs_count > 0 && cStats.vs != nil {
		cVsSlice := unsafe.Slice(cStats.vs, cStats.vs_count)
		stats.Vs = make([]NamedVsStats, cStats.vs_count)
		for i := range stats.Vs {
			stats.Vs[i] = *cToGo_NamedVsStats(&cVsSlice[i])
		}
	}

	return stats
}

func cToGo_L4Stats(cStats *C.struct_balancer_l4_stats) L4Stats {
	return L4Stats{
		IncomingPackets:  uint64(cStats.incoming_packets),
		SelectVsFailed:   uint64(cStats.select_vs_failed),
		InvalidPackets:   uint64(cStats.invalid_packets),
		SelectRealFailed: uint64(cStats.select_real_failed),
		OutgoingPackets:  uint64(cStats.outgoing_packets),
	}
}

func cToGo_IcmpStats(cStats *C.struct_balancer_icmp_stats) IcmpStats {
	return IcmpStats{
		IncomingPackets:           uint64(cStats.incoming_packets),
		SrcNotAllowed:             uint64(cStats.src_not_allowed),
		EchoResponses:             uint64(cStats.echo_responses),
		PayloadTooShortIp:         uint64(cStats.payload_too_short_ip),
		UnmatchingSrcFromOriginal: uint64(cStats.unmatching_src_from_original),
		PayloadTooShortPort:       uint64(cStats.payload_too_short_port),
		UnexpectedTransport:       uint64(cStats.unexpected_transport),
		UnrecognizedVs:            uint64(cStats.unrecognized_vs),
		ForwardedPackets:          uint64(cStats.forwarded_packets),
		BroadcastedPackets:        uint64(cStats.broadcasted_packets),
		PacketClonesSent:          uint64(cStats.packet_clones_sent),
		PacketClonesReceived:      uint64(cStats.packet_clones_received),
		PacketCloneFailures:       uint64(cStats.packet_clone_failures),
	}
}

func cToGo_CommonStats(cStats *C.struct_balancer_common_stats) CommonStats {
	return CommonStats{
		IncomingPackets:        uint64(cStats.incoming_packets),
		IncomingBytes:          uint64(cStats.incoming_bytes),
		UnexpectedNetworkProto: uint64(cStats.unexpected_network_proto),
		DecapSuccessful:        uint64(cStats.decap_successful),
		DecapFailed:            uint64(cStats.decap_failed),
		OutgoingPackets:        uint64(cStats.outgoing_packets),
		OutgoingBytes:          uint64(cStats.outgoing_bytes),
	}
}

func cToGo_NamedVsStats(cStats *C.struct_named_vs_stats) *NamedVsStats {
	if cStats == nil {
		return nil
	}

	stats := &NamedVsStats{
		Identifier: cToGo_VsIdentifier(cStats.identifier),
		Stats: VsStats{
			IncomingPackets:      uint64(cStats.stats.incoming_packets),
			IncomingBytes:        uint64(cStats.stats.incoming_bytes),
			PacketSrcNotAllowed:  uint64(cStats.stats.packet_src_not_allowed),
			NoReals:              uint64(cStats.stats.no_reals),
			OpsPackets:           uint64(cStats.stats.ops_packets),
			SessionTableOverflow: uint64(cStats.stats.session_table_overflow),
			EchoIcmpPackets:      uint64(cStats.stats.echo_icmp_packets),
			ErrorIcmpPackets:     uint64(cStats.stats.error_icmp_packets),
			RealIsDisabled:       uint64(cStats.stats.real_is_disabled),
			RealIsRemoved:        uint64(cStats.stats.real_is_removed),
			NotRescheduledPackets: uint64(
				cStats.stats.not_rescheduled_packets,
			),
			BroadcastedIcmpPackets: uint64(
				cStats.stats.broadcasted_icmp_packets,
			),
			CreatedSessions: uint64(cStats.stats.created_sessions),
			OutgoingPackets: uint64(cStats.stats.outgoing_packets),
			OutgoingBytes:   uint64(cStats.stats.outgoing_bytes),
		},
	}

	// Convert reals stats array
	if cStats.reals_count > 0 {
		cRealsSlice := unsafe.Slice(cStats.reals, cStats.reals_count)
		stats.Reals = make([]struct {
			Dst   netip.Addr
			Stats RealStats
		}, cStats.reals_count)

		for i := range stats.Reals {
			relative := cToGo_RelativeRealIdentifier(cRealsSlice[i].real)
			stats.Reals[i].Dst = relative.Addr
			stats.Reals[i].Stats = RealStats{
				PacketsRealDisabled: uint64(
					cRealsSlice[i].stats.packets_real_disabled,
				),
				OpsPackets: uint64(cRealsSlice[i].stats.ops_packets),
				ErrorIcmpPackets: uint64(
					cRealsSlice[i].stats.error_icmp_packets,
				),
				CreatedSessions: uint64(
					cRealsSlice[i].stats.created_sessions,
				),
				Packets: uint64(cRealsSlice[i].stats.packets),
				Bytes:   uint64(cRealsSlice[i].stats.bytes),
			}
		}
	}

	return stats
}

// BalancerGraph conversions

func cToGo_BalancerGraph(cGraph *C.struct_balancer_graph) *BalancerGraph {
	if cGraph == nil {
		return nil
	}

	graph := &BalancerGraph{}

	// Convert VS array
	if cGraph.vs_count > 0 && cGraph.vs != nil {
		cVsSlice := unsafe.Slice(cGraph.vs, cGraph.vs_count)
		graph.VirtualServices = make([]GraphVs, cGraph.vs_count)
		for i := range graph.VirtualServices {
			graph.VirtualServices[i] = *cToGo_GraphVs(&cVsSlice[i])
		}
	}

	return graph
}

func cToGo_GraphVs(cVs *C.struct_graph_vs) *GraphVs {
	vs := &GraphVs{
		Identifier: cToGo_VsIdentifier(cVs.identifier),
	}

	// Convert reals array
	if cVs.real_count > 0 && cVs.reals != nil {
		cRealsSlice := unsafe.Slice(cVs.reals, cVs.real_count)
		vs.Reals = make([]GraphReal, cVs.real_count)
		for i := range vs.Reals {
			vs.Reals[i] = cToGo_GraphReal(&cRealsSlice[i])
		}
	}

	return vs
}

func cToGo_GraphReal(cReal *C.struct_graph_real) GraphReal {
	return GraphReal{
		Identifier: cToGo_RelativeRealIdentifier(cReal.identifier),
		Weight:     uint16(cReal.weight),
		Enabled:    bool(cReal.enabled),
	}
}
