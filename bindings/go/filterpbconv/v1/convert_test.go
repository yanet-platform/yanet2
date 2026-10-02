package filterpbconv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	filterpbconv "github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
)

// Test_ToProtocolRanges_ExpandsStructuredEntries pins the packed
// encoding every structured protocol entry expands to: an
// unconstrained entry to the whole protocol block, a TCP flag mask to
// the runs of flag bytes it admits, ICMP types to their blocks.
func Test_ToProtocolRanges_ExpandsStructuredEntries(t *testing.T) {
	tests := []struct {
		name     string
		entries  []*filterpb.Protocol
		expected filter.ProtoRanges
	}{
		{
			name: "plain protocol covers every subtype byte",
			entries: []*filterpb.Protocol{
				{Number: 17},
			},
			expected: filter.ProtoRanges{
				filter.NewProtoRange(17, filter.AnySubtype()),
			},
		},
		{
			name: "tcp exact flag byte",
			entries: []*filterpb.Protocol{
				{Number: 6, Tcp: &filterpb.TcpFlags{Flags: 0x02, Mask: 0xff}},
			},
			expected: filter.ProtoRanges{
				filter.NewProtoRange(6, filter.ExactSubtype(0x02)),
			},
		},
		{
			name: "tcp one examined bit opens a run per block",
			entries: []*filterpb.Protocol{
				{Number: 6, Tcp: &filterpb.TcpFlags{Flags: 0x10, Mask: 0x10}},
			},
			expected: func() filter.ProtoRanges {
				var ranges filter.ProtoRanges
				for block := range 8 {
					from := uint32(block)<<5 | 0x10
					ranges = append(
						ranges,
						filter.NewProtoRange(6, filter.RangeSubtype(uint8(from), uint8(from+0x0f))),
					)
				}
				return ranges
			}(),
		},
		{
			name: "tcp mask of one low bit alternates every other byte",
			entries: []*filterpb.Protocol{
				{Number: 6, Tcp: &filterpb.TcpFlags{Flags: 0x01, Mask: 0x01}},
			},
			expected: func() filter.ProtoRanges {
				var ranges filter.ProtoRanges
				for value := 0x01; value < 0x100; value += 2 {
					ranges = append(
						ranges,
						filter.NewProtoRange(6, filter.ExactSubtype(uint8(value))),
					)
				}
				return ranges
			}(),
		},
		{
			name: "tcp zero mask admits every flag byte",
			entries: []*filterpb.Protocol{
				{Number: 6, Tcp: &filterpb.TcpFlags{}},
			},
			expected: filter.ProtoRanges{
				filter.NewProtoRange(6, filter.AnySubtype()),
			},
		},
		{
			name: "icmp and icmpv6 types",
			entries: []*filterpb.Protocol{
				{Number: 1, IcmpTypes: []*filterpb.IcmpTypeRange{{From: 8, To: 8}}},
				{Number: 58, Icmp6Types: []*filterpb.IcmpTypeRange{{From: 128, To: 129}}},
			},
			expected: filter.ProtoRanges{
				filter.NewProtoRange(1, filter.ExactSubtype(8)),
				filter.NewProtoRange(58, filter.RangeSubtype(128, 129)),
			},
		},
		{
			name: "repeated entries accumulate",
			entries: []*filterpb.Protocol{
				{Number: 6, Tcp: &filterpb.TcpFlags{Flags: 0x10, Mask: 0xff}},
				{Number: 1},
			},
			expected: filter.ProtoRanges{
				filter.NewProtoRange(6, filter.ExactSubtype(0x10)),
				filter.NewProtoRange(1, filter.AnySubtype()),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ranges, err := filterpbconv.ToProtocolRanges(tc.entries)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, ranges)
		})
	}
}

// Test_ToProtocolRanges_RejectsMismatchedEntries verifies that an
// entry whose specific conditions belong to another protocol, or whose
// values do not fit their byte, is refused as InvalidArgument.
func Test_ToProtocolRanges_RejectsMismatchedEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []*filterpb.Protocol
	}{
		{
			name:    "number exceeds one byte",
			entries: []*filterpb.Protocol{{Number: 256}},
		},
		{
			name: "tcp flags on icmp",
			entries: []*filterpb.Protocol{
				{Number: 1, Tcp: &filterpb.TcpFlags{Flags: 0x02, Mask: 0x02}},
			},
		},
		{
			name: "icmp types on tcp",
			entries: []*filterpb.Protocol{
				{Number: 6, IcmpTypes: []*filterpb.IcmpTypeRange{{From: 8, To: 8}}},
			},
		},
		{
			name: "icmpv6 types on icmp",
			entries: []*filterpb.Protocol{
				{Number: 1, Icmp6Types: []*filterpb.IcmpTypeRange{{From: 135, To: 135}}},
			},
		},
		{
			name: "tcp flags outside the mask",
			entries: []*filterpb.Protocol{
				{Number: 6, Tcp: &filterpb.TcpFlags{Flags: 0x12, Mask: 0x02}},
			},
		},
		{
			name: "tcp flags beyond one byte",
			entries: []*filterpb.Protocol{
				{Number: 6, Tcp: &filterpb.TcpFlags{Flags: 0x100, Mask: 0x102}},
			},
		},
		{
			name: "icmp type beyond one byte",
			entries: []*filterpb.Protocol{
				{Number: 1, IcmpTypes: []*filterpb.IcmpTypeRange{{From: 0, To: 256}}},
			},
		},
		{
			name: "icmp type range inverted",
			entries: []*filterpb.Protocol{
				{Number: 1, IcmpTypes: []*filterpb.IcmpTypeRange{{From: 9, To: 8}}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := filterpbconv.ToProtocolRanges(tc.entries)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}
