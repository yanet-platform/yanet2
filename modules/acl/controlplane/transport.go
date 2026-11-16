package acl

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

// transportsToProtoRanges spells the authored transport protocols of a
// rule as the packed protocol ranges the compile consumes: every
// transport specific byte range of a protocol crosses into the low
// byte of its encoded ranges, and a protocol without any is the whole
// byte.
//
// Exactly one of the transports and the packed proto_ranges may be
// authored, and the transports belong to the l4 layer alone: a rule of
// another declared kind authoring them is a configuration error.
func transportsToProtoRanges(rule *aclpb.Rule) (filter.ProtoRanges, error) {
	transports := rule.GetTransports()
	if len(transports) == 0 {
		return nil, nil
	}

	switch rule.GetKind() {
	case aclpb.RuleKind_RULE_KIND_UNSPECIFIED, aclpb.RuleKind_RULE_KIND_L4:
	case aclpb.RuleKind_RULE_KIND_L2:
		return nil, status.Errorf(codes.InvalidArgument, "an l2 rule carries no transport protocol")
	case aclpb.RuleKind_RULE_KIND_L3:
		return nil, status.Errorf(codes.InvalidArgument, "an l3 rule carries no transport protocol")
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unknown rule kind %d", rule.GetKind())
	}

	if len(rule.GetProtoRanges()) > 0 {
		return nil, status.Errorf(codes.InvalidArgument, "a rule carries both transports and proto ranges")
	}

	// A generic protocol resolves through the plain path alone, which a
	// rule with a transport byte condition never takes: the tcp and the
	// icmp paths alone have leaves. A rule mixing the two shapes would
	// silently drop its generic protocols, so it is rejected and must be
	// spelled as two rules.
	generic, byteConstrained := false, false
	for _, transport := range transports {
		switch protocol := transport.GetProtocol().(type) {
		case *aclpb.Transport_Ipproto:
			generic = true
		case *aclpb.Transport_Tcp:
			byteConstrained = byteConstrained || len(protocol.Tcp.GetFlags()) > 0
		case *aclpb.Transport_Icmp:
			byteConstrained = byteConstrained || len(protocol.Icmp.GetTypes()) > 0
		case *aclpb.Transport_Icmpv6:
			byteConstrained = byteConstrained || len(protocol.Icmpv6.GetTypes()) > 0
		}
	}
	if generic && byteConstrained {
		return nil, status.Errorf(codes.InvalidArgument,
			"a rule mixing a transport byte condition with a generic protocol must be split into two rules")
	}

	ranges := make([]filter.ProtoRange, 0, len(transports))
	for idx, transport := range transports {
		added, err := transportToProtoRanges(transport)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "transport %d: %v", idx, err)
		}
		ranges = append(ranges, added...)
	}
	return ranges, nil
}

// transportToProtoRanges encodes one transport protocol as its packed
// protocol ranges.
func transportToProtoRanges(transport *aclpb.Transport) ([]filter.ProtoRange, error) {
	switch protocol := transport.GetProtocol().(type) {
	case *aclpb.Transport_Tcp:
		return byteRanges(6, "tcp flags", protocol.Tcp.GetFlags())
	case *aclpb.Transport_Icmp:
		return byteRanges(1, "icmp types", protocol.Icmp.GetTypes())
	case *aclpb.Transport_Icmpv6:
		return byteRanges(58, "icmpv6 types", protocol.Icmpv6.GetTypes())
	case *aclpb.Transport_Udp:
		// UDP carries no transport specific byte: the whole one.
		return []filter.ProtoRange{{From: 17 << 8, To: 17<<8 | 0xff}}, nil
	case *aclpb.Transport_Ipproto:
		number := protocol.Ipproto
		if number > 0xff {
			return nil, fmt.Errorf("ipproto %d is out of the protocol byte", number)
		}
		return []filter.ProtoRange{{From: uint16(number) << 8, To: uint16(number)<<8 | 0xff}}, nil
	default:
		return nil, fmt.Errorf("no transport protocol authored")
	}
}

// byteRanges encodes the transport specific byte ranges of one
// protocol; an absent set is the whole byte.
func byteRanges(proto uint32, what string, ranges []*filterpb.ByteRange) ([]filter.ProtoRange, error) {
	if len(ranges) == 0 {
		return []filter.ProtoRange{{From: uint16(proto) << 8, To: uint16(proto)<<8 | 0xff}}, nil
	}

	encoded := make([]filter.ProtoRange, 0, len(ranges))
	for _, r := range ranges {
		from, to := r.GetFrom(), r.GetTo()
		if from > 0xff || to > 0xff || from > to {
			return nil, fmt.Errorf("%s range %d-%d is out of the transport byte", what, from, to)
		}
		encoded = append(encoded, filter.ProtoRange{
			From: uint16(proto)<<8 | uint16(from),
			To:   uint16(proto)<<8 | uint16(to),
		})
	}
	return encoded, nil
}
