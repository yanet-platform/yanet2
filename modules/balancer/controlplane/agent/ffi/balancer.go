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
#include "modules/balancer/controlplane/api/graph.h"
#include "modules/balancer/controlplane/api/session.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/diag/diag.h"
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

// MaxRealWeight is the maximum allowed scheduler weight for a real server.
// This value is taken from the C API (MAX_REAL_WEIGHT in real.h).
const MaxRealWeight uint16 = uint16(C.MAX_REAL_WEIGHT)

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

// ListBalancers returns all balancers registered in the given agent.
func ListBalancers(agent *yanet.Agent) []Balancer {
	var count C.size_t
	arr := C.balancers((*C.struct_agent)(agent.AsRawPtr()), &count)
	defer func() {
		if arr != nil {
			C.free(unsafe.Pointer(arr))
		}
	}()

	n := int(count)
	if n == 0 || arr == nil {
		return nil
	}

	cArr := unsafe.Slice((**C.struct_balancer_handle)(unsafe.Pointer(arr)), n)
	out := make([]Balancer, 0, n)
	for i := range n {
		if cArr[i] != nil {
			out = append(out, Balancer{h: cArr[i]})
		}
	}
	return out
}

// Create creates and registers a new balancer instance.
func Create(
	agent *yanet.Agent,
	name string,
	cfg BalancerConfig,
) (Balancer, error) {
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

	h := C.balancer_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		cCfg,
		&diag,
	)
	if h == nil {
		if msg := C.diag_take_msg(&diag); msg != nil {
			defer C.free(unsafe.Pointer(msg))
			return Balancer{}, fmt.Errorf(
				"balancer_create: %s",
				C.GoString(msg),
			)
		}
		return Balancer{}, fmt.Errorf("balancer_create failed")
	}
	return Balancer{h: h}, nil
}

// UpdateHandler updates packet handler configuration of an existing balancer.
func (b Balancer) UpdateHandler(cfg PacketHandlerConfig) error {
	cCfg := (*C.struct_packet_handler_config)(
		C.malloc(C.size_t(C.sizeof_struct_packet_handler_config)),
	)
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
	dst.relative.ip_proto = writeNetAddr(&dst.relative.addr, id.Relative.Ip)
	// Port for the real server - use the relative port if specified, otherwise use VS port
	if id.Relative.Port != 0 {
		dst.relative.port = C.uint16_t(id.Relative.Port)
	} else {
		dst.relative.port = C.uint16_t(id.Vs.Port)
	}
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
		flags |= C.VS_PURE_L3_FLAG
	}
	if vs.Flags.FixMSS {
		flags |= C.VS_FIX_MSS_FLAG
	}
	if vs.Flags.GRE {
		flags |= C.VS_GRE_FLAG
	}
	if vs.Flags.OPS {
		flags |= C.VS_OPS_FLAG
	}
	cVs.config.flags = flags

	cVs.config.user = C.uint64_t(vs.User)

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
		cVs.config.reals = (*C.struct_named_real_config)(
			C.malloc(
				C.size_t(len(vs.Reals)) * C.sizeof_struct_named_real_config,
			),
		)
		if cVs.config.reals == nil {
			return fmt.Errorf("malloc reals failed")
		}
		cVs.config.real_count = C.size_t(len(vs.Reals))
		reals := unsafe.Slice(cVs.config.reals, len(vs.Reals))
		for i := range vs.Reals {
			r := vs.Reals[i]
			// named_real_config has dst (net_addr), ip_proto, port fields
			// Write the real's IP address into the dst union
			if r.Identifier.Relative.Ip.Is4() {
				a := r.Identifier.Relative.Ip.As4()
				C.memcpy(
					unsafe.Pointer(&reals[i].dst),
					unsafe.Pointer(&a[0]),
					4,
				)
				reals[i].ip_proto = C.IPPROTO_IP
			} else if r.Identifier.Relative.Ip.Is6() {
				a := r.Identifier.Relative.Ip.As16()
				C.memcpy(unsafe.Pointer(&reals[i].dst), unsafe.Pointer(&a[0]), 16)
				reals[i].ip_proto = C.IPPROTO_IPV6
			}
			reals[i].port = C.int(r.Identifier.Relative.Port)
			reals[i].config.weight = C.uint16_t(r.Weight)
			netToCFromAddrMask(&reals[i].config.src, r.SrcAddr, r.SrcMask)
		}
	}

	// allowed sources as ranges (use same logic as old API: start = prefix.Addr(), end = xnetip.LastAddr(prefix))
	if len(vs.AllowedSrc) > 0 {
		cVs.config.allowed_src = (*C.struct_net_addr_range)(
			C.malloc(
				C.size_t(len(vs.AllowedSrc)) * C.sizeof_struct_net_addr_range,
			),
		)
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
				C.memcpy(
					unsafe.Pointer(&rng[i].from),
					unsafe.Pointer(&s4[0]),
					4,
				)
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
		cVs.config.peers_v4 = (*C.struct_net4_addr)(
			C.malloc(C.size_t(len(vs.PeersV4)) * C.sizeof_struct_net4_addr),
		)
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
		cVs.config.peers_v6 = (*C.struct_net6_addr)(
			C.malloc(C.size_t(len(vs.PeersV6)) * C.sizeof_struct_net6_addr),
		)
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
func buildHandlerInto(
	c *C.struct_packet_handler_config,
	cfg PacketHandlerConfig,
) (func(), error) {
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
		c.decap_v4 = (*C.struct_net4_addr)(
			C.malloc(C.size_t(v4Count) * C.sizeof_struct_net4_addr),
		)
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
		c.decap_v6 = (*C.struct_net6_addr)(
			C.malloc(C.size_t(v6Count) * C.sizeof_struct_net6_addr),
		)
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
		c.vs = (*C.struct_named_vs_config)(
			C.malloc(
				C.size_t(
					len(cfg.VirtualServices),
				) * C.sizeof_struct_named_vs_config,
			),
		)
		if c.vs == nil {
			return func() {}, fmt.Errorf("malloc vs failed")
		}
		c.vs_count = C.size_t(len(cfg.VirtualServices))
		vsArr := unsafe.Slice(c.vs, len(cfg.VirtualServices))
		for i := range cfg.VirtualServices {
			// zero-initialize entry before population
			C.memset(
				unsafe.Pointer(&vsArr[i]),
				0,
				C.sizeof_struct_named_vs_config,
			)
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
func buildCBalancerConfig(
	cfg BalancerConfig,
) (*C.struct_balancer_config, func(), error) {
	c := (*C.struct_balancer_config)(
		C.malloc(C.size_t(C.sizeof_struct_balancer_config)),
	)
	if c == nil {
		return nil, nil, fmt.Errorf("malloc balancer_config failed")
	}
	C.memset(unsafe.Pointer(c), 0, C.sizeof_struct_balancer_config)

	// Access state.table_capacity using unsafe pointer arithmetic
	// since CGo may not expose the field directly
	statePtr := (*C.struct_state_config)(unsafe.Pointer(&c.state))
	statePtr.table_capacity = C.size_t(cfg.State.SessionTableCapacity)

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

	// balancer_info requires 3 arguments: handle, info struct, and current time
	now := C.uint32_t(uint32(time.Now().Unix()))
	ret := C.balancer_info(b.h, &cInfo, now)
	if ret != 0 {
		return BalancerInfo{}, readBalancerError(b)
	}
	defer C.balancer_info_free(&cInfo)

	info := BalancerInfo{
		ActiveSessions: uint64(cInfo.active_sessions),
		// balancer_info struct doesn't have stats field, we'll need to call balancer_stats separately
		Module: ModuleStats{},
	}

	if cInfo.vs_count > 0 && cInfo.vs != nil {
		vsSlice := unsafe.Slice(cInfo.vs, int(cInfo.vs_count))
		info.VsInfo = make([]VsInfo, 0, len(vsSlice))
		for i := range vsSlice {
			// named_vs_info has fields directly, not nested in .info
			info.VsInfo = append(info.VsInfo, VsInfo{
				VsIdentifier: goFromCVsIdentifier(
					&vsSlice[i].identifier,
				),
				ActiveSessions: uint64(vsSlice[i].active_sessions),
				LastPacketTimestamp: timeFromMonotonic(
					uint32(vsSlice[i].last_packet_timestamp),
				),
				// Stats need to be retrieved separately via balancer_stats
				Stats: VsStats{},
			})
		}
	}

	// Note: balancer_info struct doesn't have reals field based on the header
	// Real info would need to be retrieved through VS info or a separate API call

	return info, nil
}

// Stats returns module stats (aggregate or filtered by packet handler reference).
func (b Balancer) Stats(ref *PacketHandlerRef) (ModuleStats, error) {
	var cStats C.struct_balancer_stats
	C.memset(unsafe.Pointer(&cStats), 0, C.sizeof_struct_balancer_stats)

	var cRef *C.struct_packet_handler_ref
	if ref != nil {
		cRef = (*C.struct_packet_handler_ref)(
			C.malloc(C.sizeof_struct_packet_handler_ref),
		)
		if cRef == nil {
			return ModuleStats{}, fmt.Errorf("malloc packet_handler_ref failed")
		}
		defer C.free(unsafe.Pointer(cRef))
		C.memset(unsafe.Pointer(cRef), 0, C.sizeof_struct_packet_handler_ref)

		// Set optional filter fields
		if ref.Device != "" {
			cRef.device = C.CString(ref.Device)
			defer C.free(unsafe.Pointer(cRef.device))
		}
		if ref.Pipeline != "" {
			cRef.pipeline = C.CString(ref.Pipeline)
			defer C.free(unsafe.Pointer(cRef.pipeline))
		}
		if ref.Function != "" {
			cRef.function = C.CString(ref.Function)
			defer C.free(unsafe.Pointer(cRef.function))
		}
		if ref.Chain != "" {
			cRef.chain = C.CString(ref.Chain)
			defer C.free(unsafe.Pointer(cRef.chain))
		}
	}

	ret := C.balancer_stats(b.h, &cStats, cRef)
	if ret != 0 {
		return ModuleStats{}, readBalancerError(b)
	}
	defer C.balancer_stats_free(&cStats)

	return goModuleStatsFromC(&cStats), nil
}

func (b Balancer) ResizeSessionTable(newSize int, now uint32) error {
	rc := C.balancer_resize_session_table(
		b.h,
		C.size_t(newSize),
		C.uint32_t(now),
	)
	if rc != 0 {
		return readBalancerError(b)
	}
	return nil
}

// Config returns the current configuration of the balancer.
// Note: This function requires the balancer_config C function to be properly linked.
// If you get a linking error, ensure the balancer API library is built and linked correctly.
func (b Balancer) Config() *BalancerConfig {
	cConfig := C.struct_balancer_config{}
	C.balancer_config(b.h, &cConfig)
	return goFromCBalancerConfig(&cConfig)
}

// UpdateReals applies a batch of real server updates.
func (b Balancer) UpdateReals(updates []RealUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	// Allocate C array for updates
	cUpdates := (*C.struct_real_update)(
		C.malloc(C.size_t(len(updates)) * C.sizeof_struct_real_update),
	)
	if cUpdates == nil {
		return fmt.Errorf("malloc real_update array failed")
	}
	defer C.free(unsafe.Pointer(cUpdates))

	// Convert Go updates to C updates
	updatesSlice := unsafe.Slice(cUpdates, len(updates))
	for i, update := range updates {
		realIdentifierToC(&updatesSlice[i].identifier, update.Identifier)

		// Set weight (use sentinel value if not updating)
		if update.Weight != nil {
			updatesSlice[i].weight = C.uint16_t(*update.Weight)
		} else {
			updatesSlice[i].weight = C.DONT_UPDATE_REAL_WEIGHT
		}

		// Set enabled (use sentinel value if not updating)
		if update.Enabled != nil {
			if *update.Enabled {
				updatesSlice[i].enabled = 1
			} else {
				updatesSlice[i].enabled = 0
			}
		} else {
			updatesSlice[i].enabled = C.DONT_UPDATE_REAL_ENABLED
		}
	}

	// Call C API
	ret := C.balancer_update_reals(b.h, C.size_t(len(updates)), cUpdates)
	if ret != 0 {
		return readBalancerError(b)
	}
	return nil
}

// SessionsInfo returns information about all active sessions.
func (b Balancer) Sessions(now uint32) ([]SessionInfo, error) {
	var cSessions C.struct_sessions

	C.balancer_sessions(b.h, &cSessions, C.uint32_t(now))
	defer C.balancer_sessions_free(&cSessions)

	if cSessions.sessions_count == 0 {
		return nil, nil
	}

	sessionsSlice := unsafe.Slice(
		cSessions.sessions,
		int(cSessions.sessions_count),
	)
	sessions := make([]SessionInfo, 0, int(cSessions.sessions_count))

	for i := range sessionsSlice {
		sess := &sessionsSlice[i]

		// Extract client IP from union
		clientIP := ipFromC(
			&sess.identifier.client_ip,
			sess.identifier.real.relative.ip_proto,
		)

		sessions = append(sessions, SessionInfo{
			ClientAddr: clientIP,
			ClientPort: uint16(sess.identifier.client_port),
			Real:       goFromCRealIdentifier(&sess.identifier.real),
			CreateTimestamp: timeFromMonotonic(
				uint32(sess.info.create_timestamp),
			),
			LastPacketTimestamp: timeFromMonotonic(
				uint32(sess.info.last_packet_timestamp),
			),
			Timeout: time.Duration(sess.info.timeout) * time.Second,
		})
	}

	return sessions, nil
}

// Graph returns the complete topology of the balancer (VS -> Reals with weights and enabled state).
func (b Balancer) Graph() BalancerGraph {
	var cGraph C.struct_balancer_graph
	C.memset(unsafe.Pointer(&cGraph), 0, C.sizeof_struct_balancer_graph)

	C.balancer_graph(b.h, &cGraph)
	defer C.balancer_graph_free(&cGraph)

	graph := BalancerGraph{
		VirtualServices: make([]GraphVs, 0, int(cGraph.vs_count)),
	}

	if cGraph.vs_count == 0 {
		return graph
	}

	vsSlice := unsafe.Slice(cGraph.vs, int(cGraph.vs_count))
	for i := range vsSlice {
		cVs := &vsSlice[i]

		graphVs := GraphVs{
			Identifier: goFromCVsIdentifier(&cVs.identifier),
			Reals:      make([]GraphReal, 0, int(cVs.real_count)),
		}

		if cVs.real_count > 0 && cVs.reals != nil {
			realsSlice := unsafe.Slice(cVs.reals, int(cVs.real_count))
			for j := range realsSlice {
				cReal := &realsSlice[j]

				// Extract real IP from relative identifier
				realIP := ipFromC(
					&cReal.identifier.addr,
					cReal.identifier.ip_proto,
				)

				graphVs.Reals = append(graphVs.Reals, GraphReal{
					Identifier: realIP,
					Weight:     uint16(cReal.weight),
					Enabled:    bool(cReal.enabled),
				})
			}
		}

		graph.VirtualServices = append(graph.VirtualServices, graphVs)
	}

	return graph
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
		Relative: RelativeRealIdentifier{
			Ip:   ipFromC(&cId.relative.addr, cId.relative.ip_proto),
			Port: uint16(cId.relative.port),
		},
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
		PacketsRealDisabled: uint64(cStats.packets_real_disabled),
		OpsPackets:          uint64(cStats.ops_packets),
		ErrorIcmpPackets:    uint64(cStats.error_icmp_packets),
		CreatedSessions:     uint64(cStats.created_sessions),
		Packets:             uint64(cStats.packets),
		Bytes:               uint64(cStats.bytes),
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
			IncomingPackets: uint64(
				cStats.icmp_ipv4.incoming_packets,
			),
			SrcNotAllowed: uint64(cStats.icmp_ipv4.src_not_allowed),
			EchoResponses: uint64(cStats.icmp_ipv4.echo_responses),
			PayloadTooShortIP: uint64(
				cStats.icmp_ipv4.payload_too_short_ip,
			),
			UnmatchingSrcFromOriginal: uint64(
				cStats.icmp_ipv4.unmatching_src_from_original,
			),
			PayloadTooShortPort: uint64(
				cStats.icmp_ipv4.payload_too_short_port,
			),
			UnexpectedTransport: uint64(
				cStats.icmp_ipv4.unexpected_transport,
			),
			UnrecognizedVS: uint64(cStats.icmp_ipv4.unrecognized_vs),
			ForwardedPackets: uint64(
				cStats.icmp_ipv4.forwarded_packets,
			),
			BroadcastedPackets: uint64(
				cStats.icmp_ipv4.broadcasted_packets,
			),
			PacketClonesSent: uint64(
				cStats.icmp_ipv4.packet_clones_sent,
			),
			PacketClonesReceived: uint64(
				cStats.icmp_ipv4.packet_clones_received,
			),
			PacketCloneFailures: uint64(
				cStats.icmp_ipv4.packet_clone_failures,
			),
		},
		ICMPv6: ICMPStats{
			IncomingPackets: uint64(
				cStats.icmp_ipv6.incoming_packets,
			),
			SrcNotAllowed: uint64(cStats.icmp_ipv6.src_not_allowed),
			EchoResponses: uint64(cStats.icmp_ipv6.echo_responses),
			PayloadTooShortIP: uint64(
				cStats.icmp_ipv6.payload_too_short_ip,
			),
			UnmatchingSrcFromOriginal: uint64(
				cStats.icmp_ipv6.unmatching_src_from_original,
			),
			PayloadTooShortPort: uint64(
				cStats.icmp_ipv6.payload_too_short_port,
			),
			UnexpectedTransport: uint64(
				cStats.icmp_ipv6.unexpected_transport,
			),
			UnrecognizedVS: uint64(cStats.icmp_ipv6.unrecognized_vs),
			ForwardedPackets: uint64(
				cStats.icmp_ipv6.forwarded_packets,
			),
			BroadcastedPackets: uint64(
				cStats.icmp_ipv6.broadcasted_packets,
			),
			PacketClonesSent: uint64(
				cStats.icmp_ipv6.packet_clones_sent,
			),
			PacketClonesReceived: uint64(
				cStats.icmp_ipv6.packet_clones_received,
			),
			PacketCloneFailures: uint64(
				cStats.icmp_ipv6.packet_clone_failures,
			),
		},
		Common: CommonStats{
			IncomingPackets: uint64(cStats.common.incoming_packets),
			IncomingBytes:   uint64(cStats.common.incoming_bytes),
			UnexpectedNetworkProto: uint64(
				cStats.common.unexpected_network_proto,
			),
			DecapSuccessful: uint64(cStats.common.decap_successful),
			DecapFailed:     uint64(cStats.common.decap_failed),
			OutgoingPackets: uint64(cStats.common.outgoing_packets),
			OutgoingBytes:   uint64(cStats.common.outgoing_bytes),
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

// Helper functions for converting C structures to Go

// net4AddrToGo converts a C struct net4_addr to Go netip.Addr
func net4AddrToGo(cAddr *C.struct_net4_addr) netip.Addr {
	if cAddr == nil {
		return netip.Addr{}
	}
	b := C.GoBytes(unsafe.Pointer(&cAddr.bytes[0]), C.int(4))
	var v4 [4]byte
	copy(v4[:], b)
	return netip.AddrFrom4(v4)
}

// net6AddrToGo converts a C struct net6_addr to Go netip.Addr
func net6AddrToGo(cAddr *C.struct_net6_addr) netip.Addr {
	if cAddr == nil {
		return netip.Addr{}
	}
	b := C.GoBytes(unsafe.Pointer(&cAddr.bytes[0]), C.int(16))
	var v6 [16]byte
	copy(v6[:], b)
	return netip.AddrFrom16(v6)
}

// netToGoAddrs converts a C struct net (union) to Go addr and mask
func netToGoAddrs(
	cNet *C.struct_net,
	ipProto C.uint8_t,
) (addr, mask netip.Addr) {
	if cNet == nil {
		return netip.Addr{}, netip.Addr{}
	}

	if ipProto == C.IPPROTO_IP {
		// IPv4: read 8 bytes (4 for addr, 4 for mask)
		b := C.GoBytes(unsafe.Pointer(cNet), C.int(8))
		var addrBytes, maskBytes [4]byte
		copy(addrBytes[:], b[0:4])
		copy(maskBytes[:], b[4:8])
		return netip.AddrFrom4(addrBytes), netip.AddrFrom4(maskBytes)
	} else if ipProto == C.IPPROTO_IPV6 {
		// IPv6: read 32 bytes (16 for addr, 16 for mask)
		b := C.GoBytes(unsafe.Pointer(cNet), C.int(32))
		var addrBytes, maskBytes [16]byte
		copy(addrBytes[:], b[0:16])
		copy(maskBytes[:], b[16:32])
		return netip.AddrFrom16(addrBytes), netip.AddrFrom16(maskBytes)
	}

	return netip.Addr{}, netip.Addr{}
}

// netAddrRangeToPrefix converts a C struct net_addr_range to Go netip.Prefix
// This reconstructs a prefix from a range (start = network addr, end = last addr)
func netAddrRangeToPrefix(
	cRange *C.struct_net_addr_range,
	ipProto C.uint8_t,
) netip.Prefix {
	if cRange == nil {
		return netip.Prefix{}
	}

	start := ipFromC(&cRange.from, ipProto)
	end := ipFromC(&cRange.to, ipProto)

	// Calculate prefix bits by comparing start and end addresses
	// This is the inverse of what buildVsInto does with xnetip.LastAddr
	if start.Is4() && end.Is4() {
		s := start.As4()
		e := end.As4()
		// Find the number of matching prefix bits
		var bits int
		for bits = 0; bits < 32; bits++ {
			mask := uint32(0xFFFFFFFF) << (32 - bits)
			sVal := uint32(
				s[0],
			)<<24 | uint32(
				s[1],
			)<<16 | uint32(
				s[2],
			)<<8 | uint32(
				s[3],
			)
			eVal := uint32(
				e[0],
			)<<24 | uint32(
				e[1],
			)<<16 | uint32(
				e[2],
			)<<8 | uint32(
				e[3],
			)
			if (sVal & mask) != (eVal & mask) {
				break
			}
		}
		if bits > 0 {
			bits--
		}
		return netip.PrefixFrom(start, bits)
	} else if start.Is6() && end.Is6() {
		// For IPv6, use a simpler approach - just return the start address with /128
		// A proper implementation would calculate the actual prefix length
		return netip.PrefixFrom(start, 128)
	}

	return netip.Prefix{}
}

// goFromCRealConfig converts a C struct named_real_config to Go RealConfig
func goFromCRealConfig(
	cReal *C.struct_named_real_config,
	vsId VsIdentifier,
) RealConfig {
	// named_real_config has dst, ip_proto, port fields instead of identifier
	// Reconstruct the real identifier from these fields
	realIp := ipFromC(&cReal.dst, C.uint8_t(cReal.ip_proto))

	realId := RealIdentifier{
		Vs: vsId,
		Relative: RelativeRealIdentifier{
			Ip:   realIp,
			Port: uint16(cReal.port),
		},
	}

	// Extract source addr and mask from the union
	srcAddr, srcMask := netToGoAddrs(
		&cReal.config.src,
		C.uint8_t(cReal.ip_proto),
	)

	return RealConfig{
		Identifier: realId,
		Weight:     uint16(cReal.config.weight),
		SrcAddr:    srcAddr,
		SrcMask:    srcMask,
	}
}

// goFromCVsConfig converts a C struct named_vs_config to Go VsConfig
func goFromCVsConfig(cVs *C.struct_named_vs_config) VsConfig {
	vsId := goFromCVsIdentifier(&cVs.identifier)

	// Parse flags
	flags := VsFlags{
		PureL3: (cVs.config.flags & C.uint8_t(1<<0)) != 0,
		FixMSS: (cVs.config.flags & C.uint8_t(1<<1)) != 0,
		GRE:    (cVs.config.flags & C.uint8_t(1<<2)) != 0,
		OPS:    (cVs.config.flags & C.uint8_t(1<<3)) != 0,
	}

	// Parse scheduler
	var scheduler VsScheduler
	switch cVs.config.scheduler {
	case C.source_hash:
		scheduler = VsSchedulerSourceHash
	case C.round_robin:
		scheduler = VsSchedulerRoundRobin
	default:
		scheduler = VsSchedulerSourceHash
	}

	vs := VsConfig{
		Identifier: vsId,
		Flags:      flags,
		Scheduler:  scheduler,
	}

	// Convert reals
	if cVs.config.real_count > 0 && cVs.config.reals != nil {
		reals := unsafe.Slice(cVs.config.reals, int(cVs.config.real_count))
		vs.Reals = make([]RealConfig, 0, len(reals))
		for i := range reals {
			vs.Reals = append(vs.Reals, goFromCRealConfig(&reals[i], vsId))
		}
	}

	// Convert allowed_src ranges to prefixes
	if cVs.config.allowed_src_count > 0 && cVs.config.allowed_src != nil {
		ranges := unsafe.Slice(
			cVs.config.allowed_src,
			int(cVs.config.allowed_src_count),
		)
		vs.AllowedSrc = make([]netip.Prefix, 0, len(ranges))
		for i := range ranges {
			prefix := netAddrRangeToPrefix(&ranges[i], cVs.identifier.ip_proto)
			if prefix.IsValid() {
				vs.AllowedSrc = append(vs.AllowedSrc, prefix)
			}
		}
	}

	// Convert peers_v4
	if cVs.config.peers_v4_count > 0 && cVs.config.peers_v4 != nil {
		peers := unsafe.Slice(
			cVs.config.peers_v4,
			int(cVs.config.peers_v4_count),
		)
		vs.PeersV4 = make([]netip.Addr, 0, len(peers))
		for i := range peers {
			vs.PeersV4 = append(vs.PeersV4, net4AddrToGo(&peers[i]))
		}
	}

	// Convert peers_v6
	if cVs.config.peers_v6_count > 0 && cVs.config.peers_v6 != nil {
		peers := unsafe.Slice(
			cVs.config.peers_v6,
			int(cVs.config.peers_v6_count),
		)
		vs.PeersV6 = make([]netip.Addr, 0, len(peers))
		for i := range peers {
			vs.PeersV6 = append(vs.PeersV6, net6AddrToGo(&peers[i]))
		}
	}

	return vs
}

// goFromCPacketHandlerConfig converts a C struct packet_handler_config to Go PacketHandlerConfig
func goFromCPacketHandlerConfig(
	cHandler *C.struct_packet_handler_config,
) PacketHandlerConfig {
	cfg := PacketHandlerConfig{
		SessionsTimeouts: SessionsTimeouts{
			TcpSynAck: uint32(cHandler.sessions_timeouts.tcp_syn_ack),
			TcpSyn:    uint32(cHandler.sessions_timeouts.tcp_syn),
			TcpFin:    uint32(cHandler.sessions_timeouts.tcp_fin),
			Tcp:       uint32(cHandler.sessions_timeouts.tcp),
			Udp:       uint32(cHandler.sessions_timeouts.udp),
			Default:   uint32(cHandler.sessions_timeouts.def),
		},
		SourceIPv4: net4AddrToGo(&cHandler.source_v4),
		SourceIPv6: net6AddrToGo(&cHandler.source_v6),
	}

	// Convert decap addresses
	decapAddrs := make([]netip.Addr, 0)

	// Add IPv4 decap addresses
	if cHandler.decap_v4_count > 0 && cHandler.decap_v4 != nil {
		v4Addrs := unsafe.Slice(cHandler.decap_v4, int(cHandler.decap_v4_count))
		for i := range v4Addrs {
			decapAddrs = append(decapAddrs, net4AddrToGo(&v4Addrs[i]))
		}
	}

	// Add IPv6 decap addresses
	if cHandler.decap_v6_count > 0 && cHandler.decap_v6 != nil {
		v6Addrs := unsafe.Slice(cHandler.decap_v6, int(cHandler.decap_v6_count))
		for i := range v6Addrs {
			decapAddrs = append(decapAddrs, net6AddrToGo(&v6Addrs[i]))
		}
	}

	cfg.DecapAddresses = decapAddrs

	// Convert virtual services
	if cHandler.vs_count > 0 && cHandler.vs != nil {
		vsSlice := unsafe.Slice(cHandler.vs, int(cHandler.vs_count))
		cfg.VirtualServices = make([]VsConfig, 0, len(vsSlice))
		for i := range vsSlice {
			cfg.VirtualServices = append(
				cfg.VirtualServices,
				goFromCVsConfig(&vsSlice[i]),
			)
		}
	}

	return cfg
}

// goFromCBalancerConfig converts a C struct balancer_config to Go BalancerConfig
func goFromCBalancerConfig(cConfig *C.struct_balancer_config) *BalancerConfig {
	if cConfig == nil {
		return nil
	}

	// Access state.table_capacity using unsafe pointer arithmetic
	// since CGo may not expose the field directly
	statePtr := (*C.struct_state_config)(unsafe.Pointer(&cConfig.state))
	sessionTableCapacity := statePtr.table_capacity

	return &BalancerConfig{
		State:   StateConfig{SessionTableCapacity: uint(sessionTableCapacity)},
		Handler: goFromCPacketHandlerConfig(&cConfig.handler),
	}
}
