package aclpb

import (
	"fmt"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	fwstatepb "github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// ToC converts the proto SyncConfig to the [cfwstate.SyncConfig] value the
// C API consumes.
//
// Field numbers and types mirror modules.fwstate's SyncConfig so the two
// stay wire-compatible; the C-level coupling is via lib/fwstate/config.h.
func (m *SyncConfig) ToC() cfwstate.SyncConfig {
	if m == nil {
		return cfwstate.SyncConfig{}
	}
	var cfg cfwstate.SyncConfig
	copy(cfg.SrcAddr[:], m.GetSrcAddr().GetAddr())
	if dstEther := m.GetDstEther(); dstEther != nil {
		eui := dstEther.EUI48()
		copy(cfg.DstEther[:], eui[:])
	}
	copy(cfg.DstAddrMulticast[:], m.GetDstAddrMulticast().GetAddr())
	copy(cfg.DstAddrUnicast[:], m.GetDstAddrUnicast().GetAddr())
	cfg.PortMulticast = uint16(m.GetPortMulticast())
	cfg.PortUnicast = uint16(m.GetPortUnicast())
	cfg.TcpSynAck = m.GetTcpSynAck()
	cfg.TcpSyn = m.GetTcpSyn()
	cfg.TcpFin = m.GetTcpFin()
	cfg.Tcp = m.GetTcp()
	cfg.Udp = m.GetUdp()
	cfg.Default = m.GetDefault()
	return cfg
}

// Validate reports whether the sync config is publishable, delegating to
// the shared fwstatepb validation so ACL and FWState enforce identical
// port, required-field, and timeout constraints.
//
// Runs against the proto field values before ToC truncates ports to
// uint16, so out-of-range ports are rejected rather than silently wrapped.
func (m *SyncConfig) Validate() error {
	return m.toFWStateSyncConfig().Validate()
}

// toFWStateSyncConfig builds the equivalent fwstatepb SyncConfig.
//
// The two proto messages mirror each other field-for-field (identical
// field numbers and types, coupled through lib/fwstate/config.h); this
// copy preserves the uint32 port values before any C-level truncation so
// the shared validation can reject out-of-range ports.
func (m *SyncConfig) toFWStateSyncConfig() *fwstatepb.SyncConfig {
	if m == nil {
		return nil
	}
	return &fwstatepb.SyncConfig{
		SrcAddr:          m.SrcAddr,
		DstEther:         m.DstEther,
		DstAddrMulticast: m.DstAddrMulticast,
		PortMulticast:    m.PortMulticast,
		DstAddrUnicast:   m.DstAddrUnicast,
		PortUnicast:      m.PortUnicast,
		TcpSynAck:        m.TcpSynAck,
		TcpSyn:           m.TcpSyn,
		TcpFin:           m.TcpFin,
		Tcp:              m.Tcp,
		Udp:              m.Udp,
		Default:          m.Default,
	}
}

// ToActions converts proto actions into backend cacl.AclAction values.
//
// Returns an error if any action carries an unrecognized kind so the caller
// can reject the request rather than silently mapping the action to ALLOW.
func ToActions(protoActions []*Action) ([]cacl.AclAction, error) {
	out := make([]cacl.AclAction, len(protoActions))
	for idx, action := range protoActions {
		kind, err := action.toCAclKind()
		if err != nil {
			return nil, fmt.Errorf("action %d: %w", idx, err)
		}

		out[idx].Kind = kind
	}

	return out, nil
}

// FromActions converts backend cacl.AclAction values into proto actions.
//
// Inverse of ToActions. Unknown kinds map to ACTION_KIND_PASS (zero).
func FromActions(actions []cacl.AclAction) []*Action {
	result := make([]*Action, len(actions))
	for idx, a := range actions {
		result[idx] = &Action{}
		result[idx].setFromCAclKind(a.Kind)
	}

	return result
}

// FromRule converts a backend cacl.AclRule into its proto representation.
func FromRule(rule cacl.AclRule) *Rule {
	actions := FromActions(rule.Actions)

	devices := make([]*filterpb.Device, len(rule.Devices))
	for idx, d := range rule.Devices {
		devices[idx] = &filterpb.Device{Name: d.Name}
	}

	vlanRanges := make([]*filterpb.VlanRange, len(rule.VlanRanges))
	for idx, v := range rule.VlanRanges {
		vlanRanges[idx] = &filterpb.VlanRange{From: uint32(v.From), To: uint32(v.To)}
	}

	srcs := make([]*filterpb.IPNet, 0, len(rule.Src4s)+len(rule.Src6s))
	for _, n := range rule.Src4s {
		srcs = append(srcs, ipNetToProto(n))
	}

	for _, n := range rule.Src6s {
		srcs = append(srcs, ipNetToProto(n))
	}

	dsts := make([]*filterpb.IPNet, 0, len(rule.Dst4s)+len(rule.Dst6s))
	for _, n := range rule.Dst4s {
		dsts = append(dsts, ipNetToProto(n))
	}

	for _, n := range rule.Dst6s {
		dsts = append(dsts, ipNetToProto(n))
	}

	srcPortRanges := make([]*filterpb.PortRange, len(rule.SrcPortRanges))
	for idx, pr := range rule.SrcPortRanges {
		srcPortRanges[idx] = &filterpb.PortRange{From: uint32(pr.From), To: uint32(pr.To)}
	}

	dstPortRanges := make([]*filterpb.PortRange, len(rule.DstPortRanges))
	for idx, pr := range rule.DstPortRanges {
		dstPortRanges[idx] = &filterpb.PortRange{From: uint32(pr.From), To: uint32(pr.To)}
	}

	protoRanges := make([]*filterpb.ProtoRange, len(rule.ProtoRanges))
	for idx, pr := range rule.ProtoRanges {
		protoRanges[idx] = &filterpb.ProtoRange{From: uint32(pr.From), To: uint32(pr.To)}
	}

	return &Rule{
		Actions:       actions,
		Counter:       rule.Counter,
		Devices:       devices,
		VlanRanges:    vlanRanges,
		Srcs:          srcs,
		Dsts:          dsts,
		SrcPortRanges: srcPortRanges,
		DstPortRanges: dstPortRanges,
		ProtoRanges:   protoRanges,
	}
}

// FromRules is the slice-level wrapper of FromRule.
func FromRules(rules []cacl.AclRule) []*Rule {
	out := make([]*Rule, len(rules))
	for idx := range rules {
		out[idx] = FromRule(rules[idx])
	}

	return out
}

// setFromCAclKind sets m.Kind to the proto enum value corresponding
// to the given cacl action kind.
//
// Unrecognized cacl kinds map to ActionKind_ACTION_KIND_PASS (the proto
// zero value). FromActions does not surface an error in this direction
// because input comes from a typed Go value, not the wire.
func (m *Action) setFromCAclKind(kind uint32) {
	switch kind {
	case uint32(cacl.ActionAllow):
		m.Kind = ActionKind_ACTION_KIND_PASS
	case uint32(cacl.ActionDeny):
		m.Kind = ActionKind_ACTION_KIND_DENY
	case uint32(cacl.ActionCount):
		m.Kind = ActionKind_ACTION_KIND_COUNT
	case uint32(cacl.ActionCheckState):
		m.Kind = ActionKind_ACTION_KIND_CHECK_STATE
	case uint32(cacl.ActionCreateState):
		m.Kind = ActionKind_ACTION_KIND_CREATE_STATE
	case uint32(cacl.ActionLog):
		m.Kind = ActionKind_ACTION_KIND_LOG
	default:
		// TODO: dangerous default.
		m.Kind = ActionKind_ACTION_KIND_PASS
	}
}

// ipNetToProto converts a filter.IPNet to a filterpb.IPNet.
//
// Used internally by FromRule. Kept here until a project-wide filterpb.From*
// family lands, at which point this helper will move there.
func ipNetToProto(n filter.IPNet) *filterpb.IPNet {
	var addrBytes, maskBytes []byte
	if n.Addr.Is4() {
		a := n.Addr.As4()
		addrBytes = a[:]
		mk := n.Mask.As4()
		maskBytes = mk[:]
	} else {
		a := n.Addr.As16()
		addrBytes = a[:]
		mk := n.Mask.As16()
		maskBytes = mk[:]
	}
	return &filterpb.IPNet{Addr: addrBytes, Mask: maskBytes}
}

// toCAclKind maps the proto kind enum to the cacl action kind.
func (m *Action) toCAclKind() (uint32, error) {
	switch m.GetKind() {
	case ActionKind_ACTION_KIND_PASS:
		return uint32(cacl.ActionAllow), nil
	case ActionKind_ACTION_KIND_DENY:
		return uint32(cacl.ActionDeny), nil
	case ActionKind_ACTION_KIND_COUNT:
		return uint32(cacl.ActionCount), nil
	case ActionKind_ACTION_KIND_CHECK_STATE:
		return uint32(cacl.ActionCheckState), nil
	case ActionKind_ACTION_KIND_CREATE_STATE:
		return uint32(cacl.ActionCreateState), nil
	case ActionKind_ACTION_KIND_LOG:
		return uint32(cacl.ActionLog), nil
	default:
		return 0, fmt.Errorf("unknown action kind %d", m.GetKind())
	}
}
