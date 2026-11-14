package cacl_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

var anyPorts = filter.PortRanges{{From: 0, To: 65535}}

func mustContiguous4(s string) xnetip.Contiguous[xnetip.Network4] {
	return xnetip.MustParseContiguous4(s)
}

// TestExpandRuleKind pins the kind contract: the absent conditions
// inside the declared kind spell out as the explicit wildcards, and a
// condition authored outside the kind is an error.
func TestExpandRuleKind(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rule    cacl.ACLRule
		want    cacl.ACLRule
		wantErr string
	}{
		{
			name: "unspecified travels as authored",
			rule: cacl.ACLRule{Src4s: []xnetip.Contiguous[xnetip.Network4]{
				mustContiguous4("10.0.0.0/8"),
			}},
			want: cacl.ACLRule{Src4s: []xnetip.Contiguous[xnetip.Network4]{
				mustContiguous4("10.0.0.0/8"),
			}},
		},
		{
			name: "l2 carries every condition absent",
			rule: cacl.ACLRule{Kind: cacl.RuleKindL2},
			want: cacl.ACLRule{Kind: cacl.RuleKindL2},
		},
		{
			name: "l2 rejects networks",
			rule: cacl.ACLRule{
				Kind:  cacl.RuleKindL2,
				Src4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			},
			wantErr: "no network, protocol or port condition",
		},
		{
			name: "l2 rejects ports",
			rule: cacl.ACLRule{
				Kind:          cacl.RuleKindL2,
				SrcPortRanges: anyPorts,
			},
			wantErr: "no network, protocol or port condition",
		},
		{
			name: "l2 rejects a fragment condition",
			rule: cacl.ACLRule{
				Kind:     cacl.RuleKindL2,
				Fragment: filter.FragmentFrag,
			},
			wantErr: "fragment condition of an l2 rule",
		},
		{
			name: "l3 fills the partner side and the protocol wildcard",
			rule: cacl.ACLRule{
				Kind:  cacl.RuleKindL3,
				Src4s: []xnetip.Contiguous[xnetip.Network4]{mustContiguous4("10.0.0.0/8")},
			},
			want: cacl.ACLRule{
				Kind:  cacl.RuleKindL3,
				Src4s: []xnetip.Contiguous[xnetip.Network4]{mustContiguous4("10.0.0.0/8")},
				Dst4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				ProtoRanges: filter.ProtoRanges{
					{From: 0, To: 0xffff},
				},
			},
		},
		{
			name: "l3 fills the v6 partner side alone",
			rule: cacl.ACLRule{
				Kind:  cacl.RuleKindL3,
				Dst6s: []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2a02::/32")},
			},
			want: cacl.ACLRule{
				Kind:  cacl.RuleKindL3,
				Src6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
				Dst6s: []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2a02::/32")},
				ProtoRanges: filter.ProtoRanges{
					{From: 0, To: 0xffff},
				},
			},
		},
		{
			name: "l3 keeps the authored protocol range",
			rule: cacl.ACLRule{
				Kind:  cacl.RuleKindL3,
				Src4s: []xnetip.Contiguous[xnetip.Network4]{mustContiguous4("10.0.0.0/8")},
				Dst4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				ProtoRanges: filter.ProtoRanges{
					{From: 6 << 8, To: 6<<8 | 0xff},
				},
			},
			want: cacl.ACLRule{
				Kind:  cacl.RuleKindL3,
				Src4s: []xnetip.Contiguous[xnetip.Network4]{mustContiguous4("10.0.0.0/8")},
				Dst4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				ProtoRanges: filter.ProtoRanges{
					{From: 6 << 8, To: 6<<8 | 0xff},
				},
			},
		},
		{
			name: "l3 rejects ports",
			rule: cacl.ACLRule{
				Kind:          cacl.RuleKindL3,
				Src4s:         []xnetip.Contiguous[xnetip.Network4]{mustContiguous4("10.0.0.0/8")},
				SrcPortRanges: anyPorts,
			},
			wantErr: "no port condition",
		},
		{
			name: "l3 rejects a transport byte condition",
			rule: cacl.ACLRule{
				Kind:  cacl.RuleKindL3,
				Src4s: []xnetip.Contiguous[xnetip.Network4]{mustContiguous4("10.0.0.0/8")},
				ProtoRanges: filter.ProtoRanges{
					{From: 6 << 8, To: 6<<8 | 0x3f},
				},
			},
			wantErr: "no transport byte condition",
		},
		{
			name:    "l3 without a network side is an error",
			rule:    cacl.ACLRule{Kind: cacl.RuleKindL3},
			wantErr: "names no network side",
		},
		{
			name: "l4 fills the absent port sides",
			rule: cacl.ACLRule{
				Kind:  cacl.RuleKindL4,
				Src4s: []xnetip.Contiguous[xnetip.Network4]{mustContiguous4("10.0.0.0/8")},
				Dst4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				ProtoRanges: filter.ProtoRanges{
					{From: 17 << 8, To: 17<<8 | 0xff},
				},
				DstPortRanges: filter.PortRanges{{From: 53, To: 53}},
			},
			want: cacl.ACLRule{
				Kind:  cacl.RuleKindL4,
				Src4s: []xnetip.Contiguous[xnetip.Network4]{mustContiguous4("10.0.0.0/8")},
				Dst4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				ProtoRanges: filter.ProtoRanges{
					{From: 17 << 8, To: 17<<8 | 0xff},
				},
				SrcPortRanges: anyPorts,
				DstPortRanges: filter.PortRanges{{From: 53, To: 53}},
			},
		},
		{
			name: "l4 keeps the transport byte condition",
			rule: cacl.ACLRule{
				Kind:  cacl.RuleKindL4,
				Src4s: []xnetip.Contiguous[xnetip.Network4]{mustContiguous4("10.0.0.0/8")},
				ProtoRanges: filter.ProtoRanges{
					{From: 6 << 8, To: 6<<8 | 0x3f},
				},
			},
			want: cacl.ACLRule{
				Kind:  cacl.RuleKindL4,
				Src4s: []xnetip.Contiguous[xnetip.Network4]{mustContiguous4("10.0.0.0/8")},
				Dst4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				ProtoRanges: filter.ProtoRanges{
					{From: 6 << 8, To: 6<<8 | 0x3f},
				},
				SrcPortRanges: anyPorts,
				DstPortRanges: anyPorts,
			},
		},
		{
			name:    "unknown kind is an error",
			rule:    cacl.ACLRule{Kind: cacl.RuleKind(99)},
			wantErr: "unknown rule kind",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := cacl.ExpandRuleKind(&tc.rule)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, tc.rule)
		})
	}
}
