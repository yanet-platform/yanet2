package cbalancer2

//#cgo CFLAGS: -I../../../../../
//#cgo CFLAGS: -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/balancer2/api -lbalancer2_cp
//#cgo LDFLAGS: -L../../../../../build/filter -lfilter_compiler
//
//#include <stdlib.h>
//#include "api/agent.h"
//#include "modules/balancer2/api/controlplane.h"
import "C"

import (
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

var (
	// VSCounterPrefix is the name prefix used for per-VS counters.
	VSCounterPrefix = C.GoString(C.balancer_vs_counter_prefix)
	// VSACLCounterPrefix is the name prefix used for per-VS ACL counters.
	VSACLCounterPrefix = C.GoString(C.balancer_vs_acl_counter_prefix)
	// RealCounterPrefix is the name prefix used for per-real counters.
	RealCounterPrefix = C.GoString(C.balancer_real_counter_prefix)
	// CommonCounterName is the name of the balancer-level common counter.
	CommonCounterName = C.GoString(C.balancer_common_counter_name)
	// L4CounterName is the name of the balancer-level L4 counter.
	L4CounterName = C.GoString(C.balancer_l4_counter_name)
)

// TunnelKind selects the encapsulation used to forward client traffic to the
// selected real.
type TunnelKind int

const (
	TunnelKindIP  TunnelKind = C.balancer_tunnel_kind_ip
	TunnelKindGRE TunnelKind = C.balancer_tunnel_kind_gre
)

// VSScheduler selects the algorithm used to pick a real for a new session.
type VSScheduler int

const (
	// VSSchedulerOP is a stateless one-packet weighted round-robin scheduler
	// that does not use a session table.
	VSSchedulerOP  VSScheduler = C.balancer_vs_sched_op
	VSSchedulerWRR VSScheduler = C.balancer_vs_sched_wrr
	VSSchedulerSH  VSScheduler = C.balancer_vs_sched_sh
)

// TransportProto selects the L4 protocol of a virtual service.
type TransportProto int

const (
	TransportTCP TransportProto = C.tcp
	TransportUDP TransportProto = C.udp
)

// Balancer is an opaque handle to a balancer configuration in shared memory.
type Balancer struct {
	ptr *C.struct_balancer_handle
}

// SessionTable is an opaque handle to a session table in shared memory.
type SessionTable struct {
	ptr *C.struct_balancer_session_table
}

// SessionTableChain is an opaque handle to a session table chain in shared
// memory. A chain references session tables but does not own them; the
// referenced tables must outlive the chain.
type SessionTableChain struct {
	ptr *C.struct_balancer_session_table_chain
}

// Install installs the balancer handle in the dataplane. If a balancer with
// the same name is already installed, it is replaced and the previous handle
// becomes unused; the caller is responsible for freeing it.
func (b *Balancer) Install(agent *ffi.Agent) error {
	if b.ptr == nil {
		return errors.New("balancer handle is freed")
	}
	var cErr *C.yanet_error
	if rc := C.balancer_install((*C.struct_agent)(agent.AsRawPtr()), b.ptr, &cErr); rc != 0 {
		return fmt.Errorf("failed to install balancer: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return nil
}

// Free releases the balancer handle. The session table chain attached to the
// balancer is not freed.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (b *Balancer) Free(agent *ffi.Agent) {
	if b.ptr != nil {
		C.balancer_free((*C.struct_agent)(agent.AsRawPtr()), b.ptr)
		b.ptr = nil
	}
}

// UpdateVSRealWeights updates per-real weights for the VS at the given index.
// The weights slice must have length equal to the number of reals configured
// for the VS and be indexed in the same order they were passed at VS
// creation.
func (b *Balancer) UpdateVSRealWeights(vsIdx uint32, weights []uint32) error {
	if b.ptr == nil {
		return errors.New("balancer handle is freed")
	}

	var cWeightsPtr *C.uint32_t
	if len(weights) > 0 {
		cWeights := make([]C.uint32_t, len(weights))
		for i, w := range weights {
			cWeights[i] = C.uint32_t(w)
		}
		cWeightsPtr = &cWeights[0]
	}

	var cErr *C.yanet_error
	if rc := C.balancer_vs_update_real_weights(b.ptr, C.uint32_t(vsIdx), cWeightsPtr, &cErr); rc != 0 {
		return fmt.Errorf("failed to update real weights: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return nil
}

// UpdateVSRealStates updates per-real enabled flags for the VS at the given
// index. The states slice must have length equal to the number of reals
// configured for the VS and be indexed in the same order they were passed at
// VS creation.
func (b *Balancer) UpdateVSRealStates(vsIdx uint32, states []bool) error {
	if b.ptr == nil {
		return errors.New("balancer handle is freed")
	}

	var cStatesPtr *C.bool
	if len(states) > 0 {
		cStates := make([]C.bool, len(states))
		for i, s := range states {
			cStates[i] = C.bool(s)
		}
		cStatesPtr = &cStates[0]
	}

	var cErr *C.yanet_error
	if rc := C.balancer_vs_update_real_states(b.ptr, C.uint32_t(vsIdx), cStatesPtr, &cErr); rc != 0 {
		return fmt.Errorf("failed to update real states: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return nil
}

// Free releases the session table.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (t *SessionTable) Free(agent *ffi.Agent) {
	if t.ptr != nil {
		C.balancer_free_session_table(
			(*C.struct_agent)(agent.AsRawPtr()),
			t.ptr,
		)
		t.ptr = nil
	}
}

// Free releases the session table chain. The session tables it referenced
// are not freed.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (c *SessionTableChain) Free(agent *ffi.Agent) {
	if c.ptr != nil {
		C.balancer_free_session_table_chain(
			(*C.struct_agent)(agent.AsRawPtr()),
			c.ptr,
		)
		c.ptr = nil
	}
}

// PushFront pushes the given table as the new front (primary) session table.
// Workers look up sessions in the front table first and fall back to the
// previous (back) table; new sessions are always created in the front table.
// Returns an error if two tables are already attached.
func (c *SessionTableChain) PushFront(table *SessionTable) error {
	if c.ptr == nil {
		return errors.New("chain is freed")
	}
	if table == nil || table.ptr == nil {
		return errors.New("table is nil or freed")
	}
	var cErr *C.yanet_error
	if rc := C.balancer_session_table_chain_push_front(c.ptr, table.ptr, &cErr); rc != 0 {
		return fmt.Errorf("failed to push front session table: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return nil
}

// PopBack detaches the back session table. Returns an error if only one
// session table is attached.
func (c *SessionTableChain) PopBack() error {
	if c.ptr == nil {
		return errors.New("chain is freed")
	}
	var cErr *C.yanet_error
	if rc := C.balancer_session_table_chain_pop_back(c.ptr, &cErr); rc != 0 {
		return fmt.Errorf("failed to pop back session table: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return nil
}

// cAllowedSources pairs a balancer_allowed_sources C struct with the C string
// memory referenced by its tag field, so the latter can be released with the
// former.
type cAllowedSources struct {
	c   C.struct_balancer_allowed_sources
	tag *C.char
}

func (a *AllowedSources) cBuild(pinner *runtime.Pinner) cAllowedSources {
	var out cAllowedSources

	filter.CBuildNet4s(&out.c.net4s, a.Net4s, pinner)
	filter.CBuildNet6s(&out.c.net6s, a.Net6s, pinner)
	filter.CBuildPortRanges(&out.c.port_ranges, a.PortRanges, pinner)

	if a.Tag != "" {
		out.tag = C.CString(a.Tag)
		out.c.tag = out.tag
	}
	return out
}

func (a *cAllowedSources) free() {
	if a.tag != nil {
		C.free(unsafe.Pointer(a.tag))
		a.tag = nil
	}
}

// cVSConfig owns the C strings referenced by a balancer_vs_config during a
// balancer_create call, transitively through the cAllowedSources entries it
// holds.
type cVSConfig struct {
	c       C.struct_balancer_vs_config
	allowed []cAllowedSources
}

func (v *VSConfig) cBuild(pinner *runtime.Pinner) (cVSConfig, error) {
	var out cVSConfig

	cAddr, family := netipToCNetAddr(v.Dst)
	out.c.dst = cAddr
	out.c.ip_family = family
	out.c.port = C.uint16_t(v.Port)
	out.c.transport = C.enum_transport_proto(v.Transport)
	out.c.scheduler = C.enum_balancer_vs_sched(v.Scheduler)
	out.c.tunnel = C.enum_balancer_tunnel_kind(v.Tunnel)
	out.c.fix_mss = C.bool(v.FixMSS)

	if len(v.Reals) > 0 {
		cReals := make([]C.struct_balancer_real_config, len(v.Reals))
		for i := range v.Reals {
			cReal, err := v.Reals[i].cBuild()
			if err != nil {
				return cVSConfig{}, fmt.Errorf("real[%d]: %w", i, err)
			}
			cReals[i] = cReal
		}
		pinner.Pin(&cReals[0])
		out.c.reals = &cReals[0]
		out.c.real_count = C.size_t(len(v.Reals))
	}

	if len(v.AllowedSources) > 0 {
		out.allowed = make([]cAllowedSources, len(v.AllowedSources))
		cAS := make([]C.struct_balancer_allowed_sources, len(v.AllowedSources))
		for i := range v.AllowedSources {
			out.allowed[i] = v.AllowedSources[i].cBuild(pinner)
			cAS[i] = out.allowed[i].c
		}
		pinner.Pin(&cAS[0])
		out.c.allowed_sources = &cAS[0]
		out.c.allowed_sources_count = C.size_t(len(v.AllowedSources))
	}

	return out, nil
}

func (v *cVSConfig) free() {
	for i := range v.allowed {
		v.allowed[i].free()
	}
	v.allowed = nil
}

func (r *RealConfig) cBuild() (C.struct_balancer_real_config, error) {
	cDst, family := netipToCNetAddr(r.Dst)
	cSrc, err := netWithMaskToCNet(r.Src)
	if err != nil {
		return C.struct_balancer_real_config{}, fmt.Errorf("source: %w", err)
	}
	return C.struct_balancer_real_config{
		dst:       cDst,
		src:       cSrc,
		ip_family: family,
	}, nil
}

func (t SessionTimeouts) toC() C.struct_balancer_session_timeouts {
	return C.struct_balancer_session_timeouts{
		tcp_syn_ack: C.uint32_t(t.TCPSynAck),
		tcp_syn:     C.uint32_t(t.TCPSyn),
		tcp_fin:     C.uint32_t(t.TCPFin),
		tcp:         C.uint32_t(t.TCP),
		udp:         C.uint32_t(t.UDP),
	}
}

func createBalancer(
	agent *ffi.Agent,
	name string,
	chain *SessionTableChain,
	timeouts SessionTimeouts,
	vs []VSConfig,
) (*Balancer, error) {
	if chain == nil || chain.ptr == nil {
		return nil, errors.New("session table chain is nil or freed")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	cTimeouts := timeouts.toC()

	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cVSConfigs := make([]cVSConfig, len(vs))
	defer func() {
		for i := range cVSConfigs {
			cVSConfigs[i].free()
		}
	}()

	var cVSPtr *C.struct_balancer_vs_config
	if len(vs) > 0 {
		cVS := make([]C.struct_balancer_vs_config, len(vs))
		for i := range vs {
			cfg, err := vs[i].cBuild(pinner)
			if err != nil {
				return nil, fmt.Errorf("vs[%d]: %w", i, err)
			}
			cVSConfigs[i] = cfg
			cVS[i] = cfg.c
		}
		pinner.Pin(&cVS[0])
		cVSPtr = &cVS[0]
	}

	var cErr *C.yanet_error
	ptr := C.balancer_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		chain.ptr,
		&cTimeouts,
		cVSPtr,
		C.uint32_t(len(vs)),
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to create balancer: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return &Balancer{ptr: ptr}, nil
}

func createSessionTable(agent *ffi.Agent, capacity uint64) (*SessionTable, error) {
	var cErr *C.yanet_error
	ptr := C.balancer_create_session_table(
		(*C.struct_agent)(agent.AsRawPtr()),
		C.size_t(capacity),
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to create session table: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return &SessionTable{ptr: ptr}, nil
}

func createSessionTableChain(agent *ffi.Agent, front *SessionTable) (*SessionTableChain, error) {
	if front == nil || front.ptr == nil {
		return nil, errors.New("front table is nil or freed")
	}
	var cErr *C.yanet_error
	ptr := C.balancer_create_session_table_chain(
		(*C.struct_agent)(agent.AsRawPtr()),
		front.ptr,
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to create session table chain: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return &SessionTableChain{ptr: ptr}, nil
}

func netipToCNetAddr(addr netip.Addr) (C.struct_net_addr, C.enum_ip_family) {
	var cAddr C.struct_net_addr
	if addr.Is4() {
		v4 := addr.As4()
		bytes := (*[4]byte)(unsafe.Pointer(&cAddr))
		*bytes = v4
		return cAddr, C.ip_family_ip4
	}
	v6 := addr.As16()
	bytes := (*[16]byte)(unsafe.Pointer(&cAddr))
	*bytes = v6
	return cAddr, C.ip_family_ip6
}

func netWithMaskToCNet(n xnetip.NetWithMask) (C.struct_net, error) {
	var cNet C.struct_net
	addr := n.Addr

	if addr.Is4() {
		if len(n.Mask) != 4 {
			return cNet, fmt.Errorf("mask length %d does not match IPv4", len(n.Mask))
		}
		v4 := addr.As4()
		layout := (*[8]byte)(unsafe.Pointer(&cNet))
		copy(layout[0:4], v4[:])
		copy(layout[4:8], n.Mask)
		return cNet, nil
	}

	if len(n.Mask) != 16 {
		return cNet, fmt.Errorf("mask length %d does not match IPv6", len(n.Mask))
	}
	v6 := addr.As16()
	layout := (*[32]byte)(unsafe.Pointer(&cNet))
	copy(layout[0:16], v6[:])
	copy(layout[16:32], n.Mask)
	return cNet, nil
}
