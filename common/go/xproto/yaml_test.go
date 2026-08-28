package xproto_test

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xproto"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	decappb "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

// prefix4 builds the masked IPv4 prefix message for a CIDR literal.
func prefix4(t *testing.T, cidr string) *commonpb.IPv4Prefix {
	t.Helper()
	prefix, err := commonpb.NewIPv4PrefixFromPrefix(netip.MustParsePrefix(cidr))
	require.NoError(t, err)
	return prefix
}

// prefix6 builds the masked IPv6 prefix message for a CIDR literal.
func prefix6(t *testing.T, cidr string) *commonpb.IPv6Prefix {
	t.Helper()
	prefix, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix(cidr))
	require.NoError(t, err)
	return prefix
}

// network4 builds the IPv4 network message for a CIDR literal.
func network4(t *testing.T, cidr string) *commonpb.IPv4Network {
	t.Helper()
	network, err := xnetip.ParseNetwork4(cidr)
	require.NoError(t, err)
	return commonpb.NewIPv4NetworkFrom4(network)
}

// network6 builds the IPv6 network message for a CIDR literal.
func network6(t *testing.T, cidr string) *commonpb.IPv6Network {
	t.Helper()
	network, err := xnetip.ParseNetwork6(cidr)
	require.NoError(t, err)
	return commonpb.NewIPv6NetworkFrom6(network)
}

// Test_Unmarshal_DecapUpdateConfigRequest verifies that a decap request
// decodes as the typed constructors build it, lists in document order.
func Test_Unmarshal_DecapUpdateConfigRequest(t *testing.T) {
	input := `
name: decap0
prefixes4:
  - 10.0.0.0/8
  - 192.0.2.0/24
prefixes6:
  - 2001:db8::/32
`
	request := &decappb.UpdateConfigRequest{}
	require.NoError(t, xproto.Unmarshal([]byte(input), request))

	want := &decappb.UpdateConfigRequest{
		Name:      "decap0",
		Prefixes4: []*commonpb.IPv4Prefix{prefix4(t, "10.0.0.0/8"), prefix4(t, "192.0.2.0/24")},
		Prefixes6: []*commonpb.IPv6Prefix{prefix6(t, "2001:db8::/32")},
	}
	require.True(t, proto.Equal(want, request), "got %v", request)
}

// Test_Unmarshal_ForwardRule verifies that a rule decodes its nested action,
// enum by name, device and vlan lists and family-typed network lists.
func Test_Unmarshal_ForwardRule(t *testing.T) {
	input := `
name: vlan-phy
rules:
  - action:
      target: 0000:81:00.0
      mode: OUT
      counter: to_0000:81:00.0
    devices:
      - name: virtio_user_kni0
    vlan_ranges:
      - {from: 0, to: 4095}
    sources4:
      - 10.0.0.0/8
    destinations6:
      - 2001:db8::/32
`
	request := &forwardpb.UpdateConfigRequest{}
	require.NoError(t, xproto.Unmarshal([]byte(input), request))

	want := &forwardpb.UpdateConfigRequest{
		Name: "vlan-phy",
		Rules: []*forwardpb.Rule{{
			Action: &forwardpb.Action{
				Target:  "0000:81:00.0",
				Mode:    forwardpb.ForwardMode_OUT,
				Counter: "to_0000:81:00.0",
			},
			Devices:       []*filterpb.Device{{Name: "virtio_user_kni0"}},
			VlanRanges:    []*filterpb.VlanRange{{From: 0, To: 4095}},
			Sources4:      []*commonpb.IPv4Network{network4(t, "10.0.0.0/8")},
			Destinations6: []*commonpb.IPv6Network{network6(t, "2001:db8::/32")},
		}},
	}
	require.True(t, proto.Equal(want, request), "got %v", request)
}

// Test_Unmarshal_FunctionWithWeightedChain verifies that a weighted chain
// of several modules decodes as the gateway API expects it.
func Test_Unmarshal_FunctionWithWeightedChain(t *testing.T) {
	input := `
id: {name: "fn:acl"}
chains:
  - weight: 1
    chain:
      name: default
      modules:
        - {type: acl, name: acl-in}
        - {type: fwstate, name: fwstate0}
`
	function := &ynpb.Function{}
	require.NoError(t, xproto.Unmarshal([]byte(input), function))

	want := &ynpb.Function{
		Id: &commonpb.FunctionId{Name: "fn:acl"},
		Chains: []*ynpb.FunctionChain{{
			Weight: 1,
			Chain: &ynpb.Chain{
				Name: "default",
				Modules: []*commonpb.ModuleId{
					{Type: "acl", Name: "acl-in"},
					{Type: "fwstate", Name: "fwstate0"},
				},
			},
		}},
	}
	require.True(t, proto.Equal(want, function), "got %v", function)
}

// Test_Unmarshal_EnumByNumber verifies that an enum number lands on the
// same value as its name.
func Test_Unmarshal_EnumByNumber(t *testing.T) {
	byName := &forwardpb.Action{}
	require.NoError(t, xproto.Unmarshal([]byte("mode: OUT\n"), byName))
	byNumber := &forwardpb.Action{}
	require.NoError(t, xproto.Unmarshal([]byte("mode: 2\n"), byNumber))

	require.True(t, proto.Equal(byName, byNumber), "got %v", byNumber)
}

// Test_Unmarshal_TextFormMessages verifies that a bare string feeds a
// message with a JSON text form, at the root and inside a oneof container.
func Test_Unmarshal_TextFormMessages(t *testing.T) {
	prefix := &commonpb.IPPrefix{}
	require.NoError(t, xproto.Unmarshal([]byte("10.0.0.0/8\n"), prefix))
	require.True(t, proto.Equal(&commonpb.IPPrefix{
		Prefix: &commonpb.IPPrefix_V4{V4: prefix4(t, "10.0.0.0/8")},
	}, prefix), "got %v", prefix)

	address := &commonpb.MACAddress{}
	require.NoError(t, xproto.Unmarshal([]byte("aa:bb:cc:dd:ee:ff\n"), address))
	require.Equal(t, "aa:bb:cc:dd:ee:ff", address.AsLogValue())
}

// Test_Unmarshal_ObjectFormMessage verifies that a message whose JSON form
// is an object decodes field by field, each field in its own text form.
func Test_Unmarshal_ObjectFormMessage(t *testing.T) {
	ipRange := &commonpb.IPRange{}
	require.NoError(t, xproto.Unmarshal([]byte("start: 10.0.0.1\nend: 10.0.0.9\n"), ipRange))

	want, err := commonpb.NewIPRange(netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.9"))
	require.NoError(t, err)
	require.True(t, proto.Equal(want, ipRange), "got %v", ipRange)
}

// Test_Unmarshal_AnchorsAndMergeKeys verifies that an alias reuses an
// anchored node and a merge key fills the fields a mapping leaves unset.
func Test_Unmarshal_AnchorsAndMergeKeys(t *testing.T) {
	input := `
rules:
  - action: &action {target: a, mode: OUT, counter: c}
    vlan_ranges: &ranges [{from: 0, to: 4095}]
  - action: {<<: *action, target: b}
    vlan_ranges: *ranges
`
	request := &forwardpb.UpdateConfigRequest{}
	require.NoError(t, xproto.Unmarshal([]byte(input), request))

	require.Len(t, request.GetRules(), 2)
	second := request.GetRules()[1]
	require.Equal(t, "b", second.GetAction().GetTarget())
	require.Equal(t, forwardpb.ForwardMode_OUT, second.GetAction().GetMode())
	require.Equal(t, "c", second.GetAction().GetCounter())
	require.True(t, proto.Equal(request.GetRules()[0].GetVlanRanges()[0], second.GetVlanRanges()[0]))
}

// Test_Unmarshal_ResolvesTags verifies that a timestamp bound for a string
// field arrives in the parser's resolved form rather than as written.
func Test_Unmarshal_ResolvesTags(t *testing.T) {
	request := &decappb.UpdateConfigRequest{}
	require.NoError(t, xproto.Unmarshal([]byte("name: 2024-01-01\n"), request))

	require.Equal(t, "2024-01-01T00:00:00Z", request.GetName())
}

// Test_Unmarshal_RejectsAliasBlowup verifies that the parser rejects a
// document whose aliases expand far beyond what it spells out.
func Test_Unmarshal_RejectsAliasBlowup(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("rules:\n")
	builder.WriteString("  - &r0 {devices: [" + strings.Repeat("{name: x}, ", 10) + "]}\n")
	for level := 1; level < 6; level++ {
		fmt.Fprintf(&builder, "  - &r%d {devices: [", level)
		for range 10 {
			fmt.Fprintf(&builder, "*r%d, ", level-1)
		}
		builder.WriteString("]}\n")
	}

	err := xproto.Unmarshal([]byte(builder.String()), &forwardpb.UpdateConfigRequest{})

	require.ErrorContains(t, err, "excessive aliasing")
}

// Test_Unmarshal_EmptyDocumentClearsMessage verifies that a stream without
// content clears the message, whatever the stream spells.
func Test_Unmarshal_EmptyDocumentClearsMessage(t *testing.T) {
	for _, input := range []string{"", "# nothing here\n", "---\n", "--- \n---\n"} {
		request := &decappb.UpdateConfigRequest{Name: "stale"}
		require.NoError(t, xproto.Unmarshal([]byte(input), request), "%q", input)
		require.True(t, proto.Equal(&decappb.UpdateConfigRequest{}, request), "%q", input)
	}
}

// Test_Unmarshal_TrailingSeparator verifies that a separator after the
// document, with or without a comment, is not a second document.
func Test_Unmarshal_TrailingSeparator(t *testing.T) {
	for _, input := range []string{"name: x\n---\n", "name: x\n---", "name: x\n---\n# end\n"} {
		request := &decappb.UpdateConfigRequest{}
		require.NoError(t, xproto.Unmarshal([]byte(input), request), "%q", input)
		require.Equal(t, "x", request.GetName(), "%q", input)
	}
}

// Test_Unmarshal_NullLeavesZeroValue verifies that a null leaves its field
// at the zero value instead of failing, as encoding/json treats null.
func Test_Unmarshal_NullLeavesZeroValue(t *testing.T) {
	request := &decappb.UpdateConfigRequest{}
	require.NoError(t, xproto.Unmarshal([]byte("name: null\nprefixes4:\n"), request))

	require.True(t, proto.Equal(&decappb.UpdateConfigRequest{}, request), "got %v", request)
}

// Test_Unmarshal_FailureBeforeJSONStageLeavesMessage verifies that a
// stream rejected before re-encoding leaves the message as it was.
func Test_Unmarshal_FailureBeforeJSONStageLeavesMessage(t *testing.T) {
	request := &decappb.UpdateConfigRequest{Name: "keep"}
	require.Error(t, xproto.Unmarshal([]byte("name: a\n---\nname: b\n"), request))

	require.Equal(t, "keep", request.GetName())
}

// Test_Unmarshal_RejectsMalformedDocuments verifies that each malformed
// document is rejected with the reason in the error.
func Test_Unmarshal_RejectsMalformedDocuments(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		message proto.Message
		wantErr string
	}{
		{
			name:    "unknown field",
			input:   "name: x\nprefixes: [10.0.0.0/8]\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: `unknown field "prefixes"`,
		},
		{
			name:    "json name spelling",
			input:   "vlanRanges: []\n",
			message: &forwardpb.Rule{},
			wantErr: `unknown field "vlanRanges"`,
		},
		{
			name:    "duplicate key",
			input:   "name: a\nname: b\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: `key "name" already defined`,
		},
		{
			name:    "second document in the stream",
			input:   "name: first\n---\nname: second\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: "more than one document",
		},
		{
			name:    "document after empty ones",
			input:   "---\n---\nname: x\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: "more than one document",
		},
		{
			name:    "syntax error in the second document",
			input:   "name: first\n---\nname: [unclosed\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: "yaml",
		},
		{
			name:    "non-string mapping key",
			input:   "443: allow\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: "such as a non-string mapping key",
		},
		{
			name:    "value JSON cannot represent",
			input:   "name: .nan\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: "such as a non-string mapping key or a NaN",
		},
		{
			name:    "alias cycle",
			input:   "rules: &loop\n  - devices: *loop\n",
			message: &forwardpb.UpdateConfigRequest{},
			wantErr: "contains itself",
		},
		{
			name:    "wrong family in a typed list",
			input:   "prefixes4:\n  - 2001:db8::/32\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: "not a valid IPv4 prefix: 2001:db8::/32",
		},
		{
			name:    "malformed text form",
			input:   "prefixes4: [10.0.0.0/33]\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: "10.0.0.0/33",
		},
		{
			name:    "object form of a text-form message",
			input:   "prefixes4:\n  - {addr: 10.0.0.0, prefix_len: 8}\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: "cannot unmarshal object",
		},
		{
			name:    "unknown enum name",
			input:   "mode: Out\n",
			message: &forwardpb.Action{},
			wantErr: "want one of NONE, IN, OUT",
		},
		{
			name:    "scalar where a sequence is expected",
			input:   "prefixes4: 10.0.0.0/8\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: "cannot unmarshal string",
		},
		{
			name:    "sequence where a message is expected",
			input:   "action: [OUT]\n",
			message: &forwardpb.Rule{},
			wantErr: "cannot unmarshal array",
		},
		{
			name:    "unquoted number in a string field",
			input:   "target: 42\n",
			message: &forwardpb.Action{},
			wantErr: "cannot unmarshal number",
		},
		{
			name:    "integer overflow",
			input:   "from: 4294967296\n",
			message: &filterpb.VlanRange{},
			wantErr: "cannot unmarshal number 4294967296",
		},
		{
			name:    "negative unsigned",
			input:   "from: -1\n",
			message: &filterpb.VlanRange{},
			wantErr: "cannot unmarshal number -1",
		},
		{
			name:    "syntax error",
			input:   "name: [unclosed\n",
			message: &decappb.UpdateConfigRequest{},
			wantErr: "yaml",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := xproto.Unmarshal([]byte(tc.input), tc.message)

			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}
