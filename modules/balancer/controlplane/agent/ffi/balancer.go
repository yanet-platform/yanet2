//go:build cgo

package ffi

/*
#cgo CFLAGS: -I../../../../../ -I../../../../../lib -I../../../../../common
#cgo LDFLAGS: -L../../../../../build/lib/controlplane/agent -lagent
#cgo LDFLAGS: -L../../../../../build/lib/controlplane/config -lconfig_cp
#cgo LDFLAGS: -L../../../../../build/lib/dataplane/config -lconfig_dp
#cgo LDFLAGS: -L../../../../../build/lib/controlplane/diag -ldiag
#cgo LDFLAGS: -L../../../../../build/common/tls_stack -ltls_stack
#cgo LDFLAGS: -L../../../../../build/lib/counters -lcounters
#cgo LDFLAGS: -L../../../../../build/lib/logging -llogging
#cgo LDFLAGS: -L../../../../../build/modules/balancer/controlplane/state -lbalancer_state
#cgo LDFLAGS: -L../../../../../build/modules/balancer/controlplane/handler -lbalancer_packet_handler
#cgo LDFLAGS: -L../../../../../build/modules/balancer/controlplane/api -lbalancer_cp

#include <stdlib.h>
#include <stddef.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <netinet/in.h>

#include "modules/balancer/controlplane/api/balancer.h"
#include "modules/balancer/controlplane/api/vs.h"
#include "modules/balancer/controlplane/api/real.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/diag/diag.h"

// Sentinel values for real updates
#define DONT_UPDATE_REAL_WEIGHT ((uint16_t)-1)
#define DONT_UPDATE_REAL_ENABLED ((uint8_t)-1)
*/
import "C"

import (
	"fmt"
	"net/netip"
	"time"
	"unsafe"

	xnetip "github.com/yanet-platform/yanet2/common/go/xnetip"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Balancer is a Go-level handle to a balancer (wraps a C handle internally).
type Balancer struct {
	h *C.struct_balancer_handle
}

// Name returns the name of the balancer instance.
func (b Balancer) Name() string {
	cName := C.balancer_name(b.h)
	if cName == nil {
		return ""
	}
	return C.GoString(cName)
}

// List returns all balancers registered in the given agent.
func List(agent *yanet.Agent) ([]Balancer, error) {
	var count C.size_t
	arr := C.balancers((*C.struct_agent)(agent.AsRawPtr()), &count)
	if arr == nil && count != 0 {
		return nil, fmt.Errorf("balancers(): NULL array with non-zero count")
	}
	defer func() {
		if arr != nil {
			C.free(unsafe.Pointer(arr))
		}
	}()

	n := int(count)
	if n == 0 || arr == nil {
		return nil, nil
	}

	cArr := unsafe.Slice((**C.struct_balancer_handle)(unsafe.Pointer(arr)), n)
	out := make([]Balancer, 0, n)
	for i := 0; i < n; i++ {
		if cArr[i] != nil {
			out = append(out, Balancer{h: cArr[i]})
		}
	}
	return out, nil
}

// Create creates and registers a new balancer instance.
func Create(agent *yanet.Agent, name string, cfg BalancerConfig) (Balancer, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	cCfg, cleanup, err := buildCBalancerConfig(cfg)
	if err != nil {
		return Balancer{}, fmt.Errorf("build balancer config: %w", err)
	}
	// Free config (and any nested allocations) after use
	defer cleanup()

	var diag C.struct_diag
	C.memset(unsafe.Pointer(&diag), 0, C.sizeof_struct_diag)

	h := C.balancer_create((*C.struct_agent)(agent.AsRawPtr()), cName, cCfg, &diag)
	if h == nil {
		if msg := C.diag_take_msg(&diag); msg != nil {
			defer C.free(unsafe.Pointer(msg))
			return Balancer{}, fmt.Errorf("balancer_create: %s", C.GoString(msg))
		}
		return Balancer{}, fmt.Errorf("balancer_create failed")
	}
	return Balancer{h: h}, nil
}

// UpdateHandler updates packet handler configuration of an existing balancer.
func (b Balancer) UpdateHandler(cfg PacketHandlerConfig) error {
	cCfg := (*C.struct_packet_handler_config)(C.malloc(C.size_t(C.sizeof_struct_packet_handler_config)))
	if cCfg == nil {
		return fmt.Errorf("malloc packet_handler_config failed")
	}
	C.memset(unsafe.Pointer(cCfg), 0, C.sizeof_struct_packet_handler_config)

	release, err := buildHandlerInto(cCfg, cfg)
	if err != nil {
		C.free(unsafe.Pointer(cCfg))
		return err
	}
	defer func() {
		release()
		C.free(unsafe.Pointer(cCfg))
	}()

	if C.balancer_update_packet_handler(b.h, cCfg) != 0 {
		return readBalancerError(b)
	}
	return nil
}

// Helpers to build C configs from Go configs

func addrToNet4C(dst *C.struct_net4_addr, addr netip.Addr) {
	var zero [4]byte
	if addr.Is4() {
		a := addr.As4()
		C.memcpy(unsafe.Pointer(&dst.bytes[0]), unsafe.Pointer(&a[0]), 4)
	} else {
		C.memcpy(unsafe.Pointer(&dst.bytes[0]), unsafe.Pointer(&zero[0]), 4)
	}
}

func addrToNet6C(dst *C.struct_net6_addr, addr netip.Addr) {
	var zero [16]byte
	if addr.Is6() {
		a := addr.As16()
		C.memcpy(unsafe.Pointer(&dst.bytes[0]), unsafe.Pointer(&a[0]), 16)
	} else {
		C.memcpy(unsafe.Pointer(&dst.bytes[0]), unsafe.Pointer(&zero[0]), 16)
	}
}

// Helpers for building VS/Real configs

// writeNetAddr writes a netip.Addr into C struct_net_addr union memory and
// returns ip_proto tag (IPPROTO_IP for IPv4, IPPROTO_IPV6 for IPv6).
func writeNetAddr(dst *C.struct_net_addr, addr netip.Addr) C.uint8_t {
	if addr.Is4() {
		a := addr.As4()
		C.memcpy(unsafe.Pointer(dst), unsafe.Pointer(&a[0]), 4)
		return C.uint8_t(C.IPPROTO_IP)
	}
	if addr.Is6() {
		a := addr.As16()
		C.memcpy(unsafe.Pointer(dst), unsafe.Pointer(&a[0]), 16)
		return C.uint8_t(C.IPPROTO_IPV6)
	}
	return 0
}

func vsIdentifierToC(dst *C.struct_vs_identifier, id VsIdentifier) {
	dst.ip_proto = writeNetAddr(&dst.addr, id.Ip)
	dst.port = C.uint16_t(id.Port)
	if id.Proto == ProtoTcp {
		dst.transport_proto = C.uint8_t(C.IPPROTO_TCP)
	} else {
		dst.transport_proto = C.uint8_t(C.IPPROTO_UDP)
	}
}

func realIdentifierToC(dst *C.struct_real_identifier, id RealIdentifier) {
	vsIdentifierToC(&dst.vs_identifier, id.Vs)
	dst.ip_proto = writeNetAddr(&dst.addr, id.Ip)
	// Use VS port for the real's destination port unless specified otherwise.
	dst.port = C.uint16_t(id.Vs.Port)
}

// netToCFromAddrMask fills struct net (union) with addr/mask bytes
// for either IPv4 (8 bytes) or IPv6 (32 bytes).
func netToCFromAddrMask(dst *C.struct_net, addr, mask netip.Addr) {
	if addr.Is4() && mask.Is4() {
		a := addr.As4()
		m := mask.As4()
		var buf [8]byte
		copy(buf[0:4], a[:])
		copy(buf[4:8], m[:])
		C.memcpy(unsafe.Pointer(dst), unsafe.Pointer(&buf[0]), 8)
	} else if addr.Is6() && mask.Is6() {
		a := addr.As16()
		m := mask.As16()
		var buf [32]byte
		copy(buf[0:16], a[:])
		copy(buf[16:32], m[:])
		C.memcpy(unsafe.Pointer(dst), unsafe.Pointer(&buf[0]), 32)
	}
}

// buildVsInto fills a single named_vs_config including reals, allowed_src and peers lists.
func buildVsInto(cVs *C.struct_named_vs_config, vs VsConfig) error {
	vsIdentifierToC(&cVs.identifier, vs.Identifier)

	// flags
	var flags C.uint8_t
	if vs.Flags.PureL3 {
		flags |= C.uint8_t(1 << 0)
	}
	if vs.Flags.FixMSS {
		flags |= C.uint8_t(1 << 1)
	}
	if vs.Flags.GRE {
		flags |= C.uint8_t(1 << 2)
	}
	if vs.Flags.OPS {
		flags |= C.uint8_t(1 << 3)
	}
	cVs.config.flags = flags

	// scheduler
	switch vs.Scheduler {
	case VsSchedulerSourceHash:
		cVs.config.scheduler = C.source_hash
	case VsSchedulerRoundRobin:
		cVs.config.scheduler = C.round_robin
	default:
		return fmt.Errorf("unknown scheduler: %d", vs.Scheduler)
	}

	// reals
	if len(vs.Reals) > 0 {
		cVs.config.reals = (*C.struct_named_real_config)(C.malloc(C.size_t(len(vs.Reals)) * C.sizeof_struct_named_real_config))
		if cVs.config.reals == nil {
			return fmt.Errorf("malloc reals failed")
		}
		cVs.config.real_count = C.size_t(len(vs.Reals))
		reals := unsafe.Slice(cVs.config.reals, len(vs.Reals))
		for i := range vs.Reals {
			r := vs.Reals[i]
			realIdentifierToC(&reals[i].identifier, r.Identifier)
			reals[i].config.weight = C.uint16_t(r.Weight)
			netToCFromAddrMask(&reals[i].config.src, r.SrcAddr, r.SrcMask)
		}
	}

	// allowed sources as ranges (use same logic as old API: start = prefix.Addr(), end = xnetip.LastAddr(prefix))
	if len(vs.AllowedSrc) > 0 {
		cVs.config.allowed_src = (*C.struct_net_addr_range)(C.malloc(C.size_t(len(vs.AllowedSrc)) * C.sizeof_struct_net_addr_range))
		if cVs.config.allowed_src == nil {
			return fmt.Errorf("malloc allowed_src failed")
		}
		cVs.config.allowed_src_count = C.size_t(len(vs.AllowedSrc))
		rng := unsafe.Slice(cVs.config.allowed_src, len(vs.AllowedSrc))
		for i := range vs.AllowedSrc {
			start := vs.AllowedSrc[i].Addr()
			end := xnetip.LastAddr(vs.AllowedSrc[i])
			if start.Is4() && end.Is4() {
				s4 := start.As4()
				e4 := end.As4()
				C.memcpy(unsafe.Pointer(&rng[i].from), unsafe.Pointer(&s4[0]), 4)
				C.memcpy(unsafe.Pointer(&rng[i].to), unsafe.Pointer(&e4[0]), 4)
			} else {
				s6 := start.As16()
				e6 := end.As16()
				C.memcpy(unsafe.Pointer(&rng[i].from), unsafe.Pointer(&s6[0]), 16)
				C.memcpy(unsafe.Pointer(&rng[i].to), unsafe.Pointer(&e6[0]), 16)
			}
		}
	}

	// peers v4
	if len(vs.PeersV4) > 0 {
		cVs.config.peers_v4 = (*C.struct_net4_addr)(C.malloc(C.size_t(len(vs.PeersV4)) * C.sizeof_struct_net4_addr))
		if cVs.config.peers_v4 == nil {
			return fmt.Errorf("malloc peers_v4 failed")
		}
		cVs.config.peers_v4_count = C.size_t(len(vs.PeersV4))
		arr := unsafe.Slice(cVs.config.peers_v4, len(vs.PeersV4))
		for i := range vs.PeersV4 {
			addrToNet4C(&arr[i], vs.PeersV4[i])
		}
	}

	// peers v6
	if len(vs.PeersV6) > 0 {
		cVs.config.peers_v6 = (*C.struct_net6_addr)(C.malloc(C.size_t(len(vs.PeersV6)) * C.sizeof_struct_net6_addr))
		if cVs.config.peers_v6 == nil {
			return fmt.Errorf("malloc peers_v6 failed")
		}
		cVs.config.peers_v6_count = C.size_t(len(vs.PeersV6))
		arr := unsafe.Slice(cVs.config.peers_v6, len(vs.PeersV6))
		for i := range vs.PeersV6 {
			addrToNet6C(&arr[i], vs.PeersV6[i])
		}
	}

	return nil
}

// buildHandlerInto fills a packet_handler_config and returns a release()
// that frees any heap allocations made for its arrays.
func buildHandlerInto(c *C.struct_packet_handler_config, cfg PacketHandlerConfig) (func(), error) {
	// Set timeouts
	c.sessions_timeouts.tcp_syn_ack = C.uint32_t(cfg.SessionsTimeouts.TcpSynAck)
	c.sessions_timeouts.tcp_syn = C.uint32_t(cfg.SessionsTimeouts.TcpSyn)
	c.sessions_timeouts.tcp_fin = C.uint32_t(cfg.SessionsTimeouts.TcpFin)
	c.sessions_timeouts.tcp = C.uint32_t(cfg.SessionsTimeouts.Tcp)
	c.sessions_timeouts.udp = C.uint32_t(cfg.SessionsTimeouts.Udp)
	c.sessions_timeouts.def = C.uint32_t(cfg.SessionsTimeouts.Default)

	// sources
	addrToNet4C(&c.source_v4, cfg.SourceIPv4)
	addrToNet6C(&c.source_v6, cfg.SourceIPv6)

	// decap arrays
	v4Count := 0
	v6Count := 0
	for _, a := range cfg.DecapAddresses {
		if a.Is4() {
			v4Count++
		} else if a.Is6() {
			v6Count++
		}
	}
	// alloc and fill v4
	if v4Count > 0 {
		c.decap_v4 = (*C.struct_net4_addr)(C.malloc(C.size_t(v4Count) * C.sizeof_struct_net4_addr))
		if c.decap_v4 == nil {
			return func() {}, fmt.Errorf("malloc decap_v4 failed")
		}
		c.decap_v4_count = C.size_t(v4Count)
		slice := unsafe.Slice(c.decap_v4, v4Count)
		idx := 0
		for _, a := range cfg.DecapAddresses {
			if a.Is4() {
				addrToNet4C(&slice[idx], a)
				idx++
			}
		}
	}
	// alloc and fill v6
	if v6Count > 0 {
		c.decap_v6 = (*C.struct_net6_addr)(C.malloc(C.size_t(v6Count) * C.sizeof_struct_net6_addr))
		if c.decap_v6 == nil {
			if c.decap_v4 != nil {
				C.free(unsafe.Pointer(c.decap_v4))
				c.decap_v4 = nil
				c.decap_v4_count = 0
			}
			return func() {}, fmt.Errorf("malloc decap_v6 failed")
		}
		c.decap_v6_count = C.size_t(v6Count)
		slice6 := unsafe.Slice(c.decap_v6, v6Count)
		idx := 0
		for _, a := range cfg.DecapAddresses {
			if a.Is6() {
				addrToNet6C(&slice6[idx], a)
				idx++
			}
		}
	}

	// Build VS list with reals, allowed_src and peers
	if len(cfg.VirtualServices) > 0 {
		c.vs = (*C.struct_named_vs_config)(C.malloc(C.size_t(len(cfg.VirtualServices)) * C.sizeof_struct_named_vs_config))
		if c.vs == nil {
			return func() {}, fmt.Errorf("malloc vs failed")
		}
		c.vs_count = C.size_t(len(cfg.VirtualServices))
		vsArr := unsafe.Slice(c.vs, len(cfg.VirtualServices))
		for i := range cfg.VirtualServices {
			// zero-initialize entry before population
			C.memset(unsafe.Pointer(&vsArr[i]), 0, C.sizeof_struct_named_vs_config)
			if err := buildVsInto(&vsArr[i], cfg.VirtualServices[i]); err != nil {
				return func() {}, fmt.Errorf("build vs[%d]: %w", i, err)
			}
		}
	}

	release := func() {
		// free decap
		if c.decap_v4 != nil {
			C.free(unsafe.Pointer(c.decap_v4))
			c.decap_v4 = nil
			c.decap_v4_count = 0
		}
		if c.decap_v6 != nil {
			C.free(unsafe.Pointer(c.decap_v6))
			c.decap_v6 = nil
			c.decap_v6_count = 0
		}
		// free VS and nested arrays
		if c.vs != nil {
			vsArr := unsafe.Slice(c.vs, int(c.vs_count))
			for i := range vsArr {
				if vsArr[i].config.reals != nil {
					C.free(unsafe.Pointer(vsArr[i].config.reals))
					vsArr[i].config.reals = nil
					vsArr[i].config.real_count = 0
				}
				if vsArr[i].config.allowed_src != nil {
					C.free(unsafe.Pointer(vsArr[i].config.allowed_src))
					vsArr[i].config.allowed_src = nil
					vsArr[i].config.allowed_src_count = 0
				}
				if vsArr[i].config.peers_v4 != nil {
					C.free(unsafe.Pointer(vsArr[i].config.peers_v4))
					vsArr[i].config.peers_v4 = nil
					vsArr[i].config.peers_v4_count = 0
				}
				if vsArr[i].config.peers_v6 != nil {
					C.free(unsafe.Pointer(vsArr[i].config.peers_v6))
					vsArr[i].config.peers_v6 = nil
					vsArr[i].config.peers_v6_count = 0
				}
			}
			C.free(unsafe.Pointer(c.vs))
			c.vs = nil
			c.vs_count = 0
		}
	}
	return release, nil
}

// buildCBalancerConfig allocates and fills a balancer_config.
// The returned cleanup frees all allocations made for the config.
func buildCBalancerConfig(cfg BalancerConfig) (*C.struct_balancer_config, func(), error) {
	c := (*C.struct_balancer_config)(C.malloc(C.size_t(C.sizeof_struct_balancer_config)))
	if c == nil {
		return nil, nil, fmt.Errorf("malloc balancer_config failed")
	}
	C.memset(unsafe.Pointer(c), 0, C.sizeof_struct_balancer_config)

	c.state.table_size = C.size_t(cfg.TableSize)

	releaseHandler, err := buildHandlerInto(&c.handler, cfg.Handler)
	if err != nil {
		C.free(unsafe.Pointer(c))
		return nil, nil, err
	}

	cleanup := func() {
		releaseHandler()
		C.free(unsafe.Pointer(c))
	}
	return c, cleanup, nil
}

// Info returns aggregated info for this balancer.
func (b Balancer) Info() (BalancerInfo, error) {
	var cInfo C.struct_balancer_info

	ret := C.balancer_info(b.h, &cInfo)
	if ret != 0 {
		return BalancerInfo{}, readBalancerError(b)
	}
	defer C.balancer_info_free(&cInfo)

	info := BalancerInfo{
		ActiveSessions: AsyncInfo{Value: uint(cInfo.active_sessions)},
		Module:         goModuleStatsFromC(&cInfo.stats),
	}

	if cInfo.vs_count > 0 && cInfo.vs != nil {
		vsSlice := unsafe.Slice(cInfo.vs, int(cInfo.vs_count))
		info.VsInfo = make([]VsInfo, 0, len(vsSlice))
		for i := range vsSlice {
			info.VsInfo = append(info.VsInfo, VsInfo{
				VsIdentifier:        goFromCVsIdentifier(&vsSlice[i].identifier),
				ActiveSessions:      AsyncInfo{Value: uint(vsSlice[i].info.active_sessions)},
				LastPacketTimestamp: timeFromMonotonic(uint32(vsSlice[i].info.last_packet_timestamp)),
				Stats:               goVsStatsFromC(&vsSlice[i].info.stats),
			})
		}
	}

	if cInfo.real_count > 0 && cInfo.reals != nil {
		realSlice := unsafe.Slice(cInfo.reals, int(cInfo.real_count))
		info.RealInfo = make([]RealInfo, 0, len(realSlice))
		for i := range realSlice {
			enabled := bool(realSlice[i].enabled)
			info.RealInfo = append(info.RealInfo, RealInfo{
				RealIdentifier:      goFromCRealIdentifier(&realSlice[i].identifier),
				ActiveSessions:      AsyncInfo{Value: uint(realSlice[i].info.active_sessions)},
				LastPacketTimestamp: timeFromMonotonic(uint32(realSlice[i].info.last_packet_timestamp)),
				Stats:               goRealStatsFromC(&realSlice[i].info.stats),
				Enabled:             enabled,
			})
		}
	}

	return info, nil
}

// Stats returns module stats (aggregate).
func (b Balancer) Stats() (ModuleStats, error) {
	var cStats C.struct_balancer_stats

	ret := C.balancer_packet_handler_stats(b.h, &cStats, nil)
	if ret != 0 {
		return ModuleStats{}, readBalancerError(b)
	}

	return goModuleStatsFromC(&cStats), nil
}

// VirtualServices returns all VS runtime infos.
func (b Balancer) VirtualServices() ([]VsInfo, error) {
	var cVs *C.struct_named_vs_info
	count := C.balancer_virtual_services_info(b.h, &cVs)
	if count < 0 {
		return nil, readBalancerError(b)
	}
	if count == 0 {
		return nil, nil
	}
	defer C.free(unsafe.Pointer(cVs))

	vsSlice := unsafe.Slice(cVs, int(count))
	out := make([]VsInfo, 0, len(vsSlice))
	for i := range vsSlice {
		out = append(out, VsInfo{
			VsIdentifier:        goFromCVsIdentifier(&vsSlice[i].identifier),
			ActiveSessions:      AsyncInfo{Value: uint(vsSlice[i].info.active_sessions)},
			LastPacketTimestamp: timeFromMonotonic(uint32(vsSlice[i].info.last_packet_timestamp)),
			Stats:               goVsStatsFromC(&vsSlice[i].info.stats),
		})
	}
	return out, nil
}

// Reals returns all real runtime infos.
func (b Balancer) Reals() ([]RealInfo, error) {
	var cReals *C.struct_named_real_info
	count := C.balancer_reals_info(b.h, &cReals)
	if count < 0 {
		return nil, readBalancerError(b)
	}
	if count == 0 {
		return nil, nil
	}
	defer C.free(unsafe.Pointer(cReals))

	realSlice := unsafe.Slice(cReals, int(count))
	out := make([]RealInfo, 0, len(realSlice))
	for i := range realSlice {
		enabled := bool(realSlice[i].enabled)
		out = append(out, RealInfo{
			RealIdentifier:      goFromCRealIdentifier(&realSlice[i].identifier),
			ActiveSessions:      AsyncInfo{Value: uint(realSlice[i].info.active_sessions)},
			LastPacketTimestamp: timeFromMonotonic(uint32(realSlice[i].info.last_packet_timestamp)),
			Stats:               goRealStatsFromC(&realSlice[i].info.stats),
			Enabled:             enabled,
		})
	}
	return out, nil
}

// Sessions enumerates active sessions. If onlyCount is true, returns count only.
func (b Balancer) Sessions(now uint32, onlyCount bool) (int, []SessionInfo, error) {
	var cSessions *C.struct_named_session_info
	var cOnly C.bool
	if onlyCount {
		cOnly = C.bool(true)
	} else {
		cOnly = C.bool(false)
	}
	count := C.balancer_sessions_info(b.h, &cSessions, C.uint32_t(now), cOnly)
	if count < 0 {
		return 0, nil, readBalancerError(b)
	}
	if count == 0 || onlyCount {
		return int(count), nil, nil
	}
	defer C.free(unsafe.Pointer(cSessions))

	sessSlice := unsafe.Slice(cSessions, int(count))
	out := make([]SessionInfo, 0, len(sessSlice))
	for i := range sessSlice {
		// Determine client IP version using real.ip_proto from the same session
		clientIP := ipFromC(&sessSlice[i].identifier.client_ip, sessSlice[i].identifier.real.ip_proto)
		out = append(out, SessionInfo{
			ClientAddr:          clientIP,
			ClientPort:          uint16(sessSlice[i].identifier.client_port),
			Real:                goFromCRealIdentifier(&sessSlice[i].identifier.real),
			CreateTimestamp:     timeFromMonotonic(uint32(sessSlice[i].info.create_timestamp)),
			LastPacketTimestamp: timeFromMonotonic(uint32(sessSlice[i].info.last_packet_timestamp)),
			Timeout:             time.Duration(sessSlice[i].info.timeout) * time.Second,
		})
	}
	return int(count), out, nil
}

func (b Balancer) ResizeSessionTable(newSize int, now uint32) error {
	rc := C.balancer_resize_session_table(b.h, C.size_t(newSize), C.uint32_t(now))
	if rc != 0 {
		return readBalancerError(b)
	}
	return nil
}

// UpdateReals applies a batch of real server updates.
func (b Balancer) UpdateReals(updates []RealUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	// For now, apply updates one by one using the handler update
	// TODO: Implement batch update using balancer_update_reals C function
	// when the FFI bindings are properly set up

	// This is a temporary workaround - we'll need to regenerate the handler config
	// The proper implementation should use the C balancer_update_reals function directly
	return fmt.Errorf("UpdateReals FFI method not yet fully implemented - use UpdateHandler instead")
}

// Conversion helpers: C -> Go

func ipFromC(cAddr *C.struct_net_addr, ipProto C.uint8_t) netip.Addr {
	// struct net_addr is a union; cgo does not expose union fields directly.
	// The union memory starts at the address of cAddr, so we can read
	// IPv4/IPv6 bytes by slicing from that pointer with appropriate length.
	switch ipProto {
	case C.IPPROTO_IP:
		b := C.GoBytes(unsafe.Pointer(cAddr), C.int(4))
		var v4 [4]byte
		copy(v4[:], b)
		return netip.AddrFrom4(v4)
	case C.IPPROTO_IPV6:
		b := C.GoBytes(unsafe.Pointer(cAddr), C.int(16))
		var v6 [16]byte
		copy(v6[:], b)
		return netip.AddrFrom16(v6)
	}
	return netip.Addr{}
}

func goProtoFromC(proto C.uint8_t) VsProto {
	if proto == C.IPPROTO_TCP {
		return ProtoTcp
	}
	return ProtoUdp
}

func goFromCVsIdentifier(cId *C.struct_vs_identifier) VsIdentifier {
	return VsIdentifier{
		Ip:    ipFromC(&cId.addr, cId.ip_proto),
		Port:  uint16(cId.port),
		Proto: goProtoFromC(cId.transport_proto),
	}
}

func goFromCRealIdentifier(cId *C.struct_real_identifier) RealIdentifier {
	return RealIdentifier{
		Vs: goFromCVsIdentifier(&cId.vs_identifier),
		Ip: ipFromC(&cId.addr, cId.ip_proto),
	}
}

func goVsStatsFromC(cStats *C.struct_vs_stats) VsStats {
	return VsStats{
		IncomingPackets:        uint64(cStats.incoming_packets),
		IncomingBytes:          uint64(cStats.incoming_bytes),
		PacketSrcNotAllowed:    uint64(cStats.packet_src_not_allowed),
		NoReals:                uint64(cStats.no_reals),
		OpsPackets:             uint64(cStats.ops_packets),
		SessionTableOverflow:   uint64(cStats.session_table_overflow),
		EchoIcmpPackets:        uint64(cStats.echo_icmp_packets),
		ErrorIcmpPackets:       uint64(cStats.error_icmp_packets),
		RealIsDisabled:         uint64(cStats.real_is_disabled),
		RealIsRemoved:          uint64(cStats.real_is_removed),
		NotRescheduledPackets:  uint64(cStats.not_rescheduled_packets),
		BroadcastedIcmpPackets: uint64(cStats.broadcasted_icmp_packets),
		CreatedSessions:        uint64(cStats.created_sessions),
		OutgoingPackets:        uint64(cStats.outgoing_packets),
		OutgoingBytes:          uint64(cStats.outgoing_bytes),
	}
}

func goRealStatsFromC(cStats *C.struct_real_stats) RealStats {
	return RealStats{
		PacketsRealDisabled:   uint64(cStats.packets_real_disabled),
		PacketsRealNotPresent: uint64(cStats.packets_real_not_present),
		OpsPackets:            uint64(cStats.ops_packets),
		ErrorIcmpPackets:      uint64(cStats.error_icmp_packets),
		CreatedSessions:       uint64(cStats.created_sessions),
		Packets:               uint64(cStats.packets),
		Bytes:                 uint64(cStats.bytes),
	}
}

func goModuleStatsFromC(cStats *C.struct_balancer_stats) ModuleStats {
	return ModuleStats{
		L4: L4Stats{
			IncomingPackets:  uint64(cStats.l4.incoming_packets),
			SelectVSFailed:   uint64(cStats.l4.select_vs_failed),
			InvalidPackets:   uint64(cStats.l4.invalid_packets),
			SelectRealFailed: uint64(cStats.l4.select_real_failed),
			OutgoingPackets:  uint64(cStats.l4.outgoing_packets),
		},
		ICMPv4: ICMPStats{
			IncomingPackets:           uint64(cStats.icmp_ipv4.incoming_packets),
			SrcNotAllowed:             uint64(cStats.icmp_ipv4.src_not_allowed),
			EchoResponses:             uint64(cStats.icmp_ipv4.echo_responses),
			PayloadTooShortIP:         uint64(cStats.icmp_ipv4.payload_too_short_ip),
			UnmatchingSrcFromOriginal: uint64(cStats.icmp_ipv4.unmatching_src_from_original),
			PayloadTooShortPort:       uint64(cStats.icmp_ipv4.payload_too_short_port),
			UnexpectedTransport:       uint64(cStats.icmp_ipv4.unexpected_transport),
			UnrecognizedVS:            uint64(cStats.icmp_ipv4.unrecognized_vs),
			ForwardedPackets:          uint64(cStats.icmp_ipv4.forwarded_packets),
			BroadcastedPackets:        uint64(cStats.icmp_ipv4.broadcasted_packets),
			PacketClonesSent:          uint64(cStats.icmp_ipv4.packet_clones_sent),
			PacketClonesReceived:      uint64(cStats.icmp_ipv4.packet_clones_received),
			PacketCloneFailures:       uint64(cStats.icmp_ipv4.packet_clone_failures),
		},
		ICMPv6: ICMPStats{
			IncomingPackets:           uint64(cStats.icmp_ipv6.incoming_packets),
			SrcNotAllowed:             uint64(cStats.icmp_ipv6.src_not_allowed),
			EchoResponses:             uint64(cStats.icmp_ipv6.echo_responses),
			PayloadTooShortIP:         uint64(cStats.icmp_ipv6.payload_too_short_ip),
			UnmatchingSrcFromOriginal: uint64(cStats.icmp_ipv6.unmatching_src_from_original),
			PayloadTooShortPort:       uint64(cStats.icmp_ipv6.payload_too_short_port),
			UnexpectedTransport:       uint64(cStats.icmp_ipv6.unexpected_transport),
			UnrecognizedVS:            uint64(cStats.icmp_ipv6.unrecognized_vs),
			ForwardedPackets:          uint64(cStats.icmp_ipv6.forwarded_packets),
			BroadcastedPackets:        uint64(cStats.icmp_ipv6.broadcasted_packets),
			PacketClonesSent:          uint64(cStats.icmp_ipv6.packet_clones_sent),
			PacketClonesReceived:      uint64(cStats.icmp_ipv6.packet_clones_received),
			PacketCloneFailures:       uint64(cStats.icmp_ipv6.packet_clone_failures),
		},
		Common: CommonStats{
			IncomingPackets:        uint64(cStats.common.incoming_packets),
			IncomingBytes:          uint64(cStats.common.incoming_bytes),
			UnexpectedNetworkProto: uint64(cStats.common.unexpected_network_proto),
			DecapSuccessful:        uint64(cStats.common.decap_successful),
			DecapFailed:            uint64(cStats.common.decap_failed),
			OutgoingPackets:        uint64(cStats.common.outgoing_packets),
			OutgoingBytes:          uint64(cStats.common.outgoing_bytes),
		},
	}
}

func timeFromMonotonic(val uint32) time.Time {
	// Monotonic seconds exposed by C API; convert to wall time seconds.
	return time.Unix(int64(val), 0)
}

func readBalancerError(b Balancer) error {
	msg := C.balancer_take_error_msg(b.h)
	if msg == nil {
		return fmt.Errorf("operation failed (no error message)")
	}
	defer C.free(unsafe.Pointer(msg))
	return fmt.Errorf("%s", C.GoString(msg))
}
