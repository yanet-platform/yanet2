package cacl

import (
	"errors"
	"fmt"

	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
)

// RuleKind declares the network layers a rule speaks about: which
// conditions of the rule are defined. The values mirror the aclpb
// RuleKind enum.
type RuleKind uint32

const (
	// RuleKindUnspecified keeps the historical inference: the kind is
	// read out of the authored conditions themselves.
	RuleKindUnspecified RuleKind = iota
	// RuleKindL2 speaks about the packets without an IP header: only
	// the devices condition is defined.
	RuleKindL2
	// RuleKindL3 speaks about the IP layer: the network, the protocol
	// and the fragment conditions are defined, the transport specific
	// ones are not.
	RuleKindL3
	// RuleKindL4 speaks about the transport layer on top of the L3
	// conditions.
	RuleKindL4
)

// wholeProtoRange spans every protocol with every transport byte: the
// no-constraint wildcard of the protocol condition.
var wholeProtoRange = filter.ProtoRanges{{From: 0, To: 0xffff}}

// ExpandRuleKind validates a rule against its declared kind and spells
// the absent conditions inside the kind out as the explicit wildcards
// the compile expects: a family of the rule participates when either
// of its sides is authored and the absent partner side becomes the
// whole address domain, an absent port side becomes the whole port
// domain, and an absent protocol set becomes the whole 16 bit range.
//
// A condition authored outside the declared kind is an error: the kind
// states the layers the rule may speak about, and a field outside them
// would be silently ignored otherwise. An unspecified kind changes
// nothing - the rule travels as authored and the compile reads the
// kind out of the conditions themselves.
func ExpandRuleKind(rule *ACLRule) error {
	switch rule.Kind {
	case RuleKindUnspecified:
		return nil

	case RuleKindL2:
		hasNets := len(rule.Src4s) > 0 || len(rule.Dst4s) > 0 ||
			len(rule.Src6s) > 0 || len(rule.Dst6s) > 0
		if hasNets || len(rule.ProtoRanges) > 0 ||
			len(rule.SrcPortRanges) > 0 || len(rule.DstPortRanges) > 0 {
			return errors.New(
				"an l2 rule carries no network, protocol or port condition",
			)
		}
		if rule.Fragment != filter.FragmentAny {
			return errors.New(
				"the fragment condition of an l2 rule must stay absent",
			)
		}
		return nil

	case RuleKindL3, RuleKindL4:
		if rule.Kind == RuleKindL3 &&
			(len(rule.SrcPortRanges) > 0 || len(rule.DstPortRanges) > 0) {
			return errors.New("an l3 rule carries no port condition")
		}
		if rule.Kind == RuleKindL3 {
			for _, r := range rule.ProtoRanges {
				if r.From&0xff != 0 || r.To&0xff != 0xff {
					return errors.New(
						"an l3 rule carries no transport byte condition",
					)
				}
			}
		}

		authored4 := len(rule.Src4s) > 0 || len(rule.Dst4s) > 0
		if !authored4 && len(rule.Src6s) == 0 && len(rule.Dst6s) == 0 {
			return errors.New("an ip rule names no network side")
		}
		if authored4 {
			if len(rule.Src4s) == 0 {
				rule.Src4s = []xnetip.Contiguous[xnetip.Network4]{
					filter.UnspecifiedIPv4,
				}
			}
			if len(rule.Dst4s) == 0 {
				rule.Dst4s = []xnetip.Contiguous[xnetip.Network4]{
					filter.UnspecifiedIPv4,
				}
			}
		}
		authored6 := len(rule.Src6s) > 0 || len(rule.Dst6s) > 0
		if authored6 {
			if len(rule.Src6s) == 0 {
				rule.Src6s = []xnetip.BiContiguous{filter.UnspecifiedIPv6}
			}
			if len(rule.Dst6s) == 0 {
				rule.Dst6s = []xnetip.BiContiguous{filter.UnspecifiedIPv6}
			}
		}
		if len(rule.ProtoRanges) == 0 {
			rule.ProtoRanges = wholeProtoRange
		}
		if rule.Kind == RuleKindL4 {
			if len(rule.SrcPortRanges) == 0 {
				rule.SrcPortRanges = filter.PortRanges{{From: 0, To: 65535}}
			}
			if len(rule.DstPortRanges) == 0 {
				rule.DstPortRanges = filter.PortRanges{{From: 0, To: 65535}}
			}
		}
		return nil

	default:
		return fmt.Errorf("unknown rule kind %d", uint32(rule.Kind))
	}
}
