package aclpb

import (
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// ToActions converts proto actions into backend ACL actions.
//
// Returns an error if any action carries an unrecognized kind so the caller
// can reject the request rather than silently mapping the action to ALLOW.
func ToActions(protoActions []*Action) ([]cacl.ACLAction, error) {
	out := make([]cacl.ACLAction, len(protoActions))
	for idx, action := range protoActions {
		kind, err := action.ToCACLKind()
		if err != nil {
			return nil, fmt.Errorf("action %d: %w", idx, err)
		}

		out[idx].Kind = kind
	}

	return out, nil
}

// FromActions converts backend ACL actions into proto actions.
//
// Inverse of ToActions. Unknown kinds map to ACTION_KIND_PASS (zero).
func FromActions(actions []cacl.ACLAction) []*Action {
	result := make([]*Action, len(actions))
	for idx, a := range actions {
		result[idx] = &Action{}
		result[idx].SetFromCACLKind(a.Kind)
	}

	return result
}

// FromRule converts a backend ACL rule into its proto representation.
func FromRule(rule cacl.ACLRule) *Rule {
	actions := FromActions(rule.Actions)

	devices := make([]*filterpb.Device, len(rule.Devices))
	for idx, d := range rule.Devices {
		devices[idx] = &filterpb.Device{Name: d.Name}
	}

	vlanRanges := make([]*filterpb.VlanRange, len(rule.VlanRanges))
	for idx, v := range rule.VlanRanges {
		vlanRanges[idx] = &filterpb.VlanRange{From: uint32(v.From), To: uint32(v.To)}
	}

	sources4 := make([]*commonpb.IPv4Network, len(rule.Src4s))
	for idx, n := range rule.Src4s {
		sources4[idx] = commonpb.NewIPv4NetworkFrom4(n.Network())
	}

	sources6 := make([]*commonpb.IPv6Network, len(rule.Src6s))
	for idx, n := range rule.Src6s {
		sources6[idx] = commonpb.NewIPv6NetworkFrom6(n.Network())
	}

	destinations4 := make([]*commonpb.IPv4Network, len(rule.Dst4s))
	for idx, n := range rule.Dst4s {
		destinations4[idx] = commonpb.NewIPv4NetworkFrom4(n.Network())
	}

	destinations6 := make([]*commonpb.IPv6Network, len(rule.Dst6s))
	for idx, n := range rule.Dst6s {
		destinations6[idx] = commonpb.NewIPv6NetworkFrom6(n.Network())
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
		Sources4:      sources4,
		Sources6:      sources6,
		Destinations4: destinations4,
		Destinations6: destinations6,
		SrcPortRanges: srcPortRanges,
		DstPortRanges: dstPortRanges,
		ProtoRanges:   protoRanges,
	}
}

// FromRules is the slice-level wrapper of FromRule.
func FromRules(rules []cacl.ACLRule) []*Rule {
	out := make([]*Rule, len(rules))
	for idx := range rules {
		out[idx] = FromRule(rules[idx])
	}

	return out
}

// SetFromCACLKind maps a backend action kind to its proto representation.
//
// Unrecognized kinds map to the zero value, which permits the packet.
func (m *Action) SetFromCACLKind(kind uint32) {
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

// ToCACLKind maps the proto kind enum to the backend action kind.
func (m *Action) ToCACLKind() (uint32, error) {
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
