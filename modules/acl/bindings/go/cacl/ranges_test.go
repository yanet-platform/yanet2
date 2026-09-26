package cacl_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

var wholePorts = filter.PortRanges{{From: 0, To: 65535}}

// TestSplitProtoRanges pins the byte split of the authored 16 bit
// protocol ranges: the high byte spans for the shared core, the low
// byte sub-spans clamped at the protocol edges for the transport
// leaves.
func TestSplitProtoRanges(t *testing.T) {
	split := cacl.SplitProtoRanges(filter.ProtoRanges{
		// Protos 1..3 with whole transport bytes.
		{From: 1 << 8, To: 3<<8 | 0xff},
		// TCP only, flags 0..0x3f.
		{From: 6 << 8, To: 6<<8 | 0x3f},
		// Protos 6..17: clamps to the whole byte at both single
		// protocol edges outside the span.
		{From: 6<<8 | 0x40, To: 17<<8 | 0x7f},
		// ICMPv6 only, types 0x80..0xff.
		{From: 58<<8 | 0x80, To: 58<<8 | 0xff},
	})

	assert.Equal(t, []cacl.LineRange{
		{From: 1, To: 3},
		{From: 6, To: 6},
		{From: 6, To: 17},
		{From: 58, To: 58},
	}, split.IpProto)

	assert.Equal(t, []cacl.LineRange{
		{From: 0, To: 0x3f},
		{From: 0x40, To: 0xff},
	}, split.TCPFlags)

	assert.Equal(t, []cacl.LineRange{
		{From: 0, To: 0xff},
		{From: 0x80, To: 0xff},
	}, split.ICMPTypes)

	assert.False(t, split.WholeL4)
}

// TestRulePathMembership pins the family path routing of one rule:
// the plain path owns the whole byte rules with whole ports and the
// fragment constrained ones, and every protocol path takes the
// remaining rules that can match a packet of its protocol.
func TestRulePathMembership(t *testing.T) {
	for _, tc := range []struct {
		name     string
		protos   filter.ProtoRanges
		srcPorts filter.PortRanges
		fragment filter.Fragment
		want     cacl.PathFlags
	}{
		{
			name:     "whole_byte_full_ports_is_plain",
			protos:   filter.ProtoRanges{{From: 0, To: 0xffff}},
			srcPorts: wholePorts,
			want:     cacl.PathFlags{Plain: true},
		},
		{
			name:     "whole_byte_partial_ports_is_tcp",
			protos:   filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
			srcPorts: filter.PortRanges{{From: 100, To: 200}},
			want:     cacl.PathFlags{TCP: true},
		},
		{
			name:   "udp_zero_subtype_partial_span_is_udp",
			protos: filter.ProtoRanges{{From: 17 << 8, To: 17<<8 | 0x7f}},
			want:   cacl.PathFlags{UDP: true},
		},
		{
			name:   "udp_subtype_without_zero_matches_nothing",
			protos: filter.ProtoRanges{{From: 17<<8 | 0x40, To: 17<<8 | 0x7f}},
			want:   cacl.PathFlags{},
		},
		{
			name: "mixed_udp_zero_and_tcp_whole_is_udp_and_tcp",
			protos: filter.ProtoRanges{
				{From: 17 << 8, To: 17 << 8},
				filter.NewProtoRange(6, filter.AnySubtype()),
			},
			want: cacl.PathFlags{TCP: true, UDP: true},
		},
		{
			name:     "icmp_partial_ports_stays_out",
			protos:   filter.ProtoRanges{filter.NewProtoRange(1, filter.ExactSubtype(8))},
			srcPorts: filter.PortRanges{{From: 100, To: 200}},
			want:     cacl.PathFlags{},
		},
		{
			name:   "icmp_whole_span_whole_byte_is_plain",
			protos: filter.ProtoRanges{{From: 1 << 8, To: 58<<8 | 0xff}},
			want:   cacl.PathFlags{Plain: true},
		},
		{
			name:   "icmp_partial_type_span_is_icmp",
			protos: filter.ProtoRanges{{From: 1 << 8, To: 1<<8 | 0x7f}},
			want:   cacl.PathFlags{ICMP: true},
		},
		{
			name: "mixed_whole_icmp_and_partial_udp_is_icmp_and_udp",
			protos: filter.ProtoRanges{
				{From: 1 << 8, To: 1<<8 | 0xff},
				{From: 17 << 8, To: 17<<8 | 0x7f},
			},
			want: cacl.PathFlags{UDP: true, ICMP: true},
		},
		{
			name:     "fragment_constrained_whole_byte_is_plain",
			protos:   filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
			fragment: filter.FragmentFrag,
			want:     cacl.PathFlags{Plain: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cacl.RulePathMembership(
				tc.protos, tc.srcPorts, wholePorts, tc.fragment,
			)
			assert.Equal(t, tc.want, got)
		})
	}
}
