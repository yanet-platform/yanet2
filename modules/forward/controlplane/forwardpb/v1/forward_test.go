package forwardpb_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xproto"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

// verifies that a mode travels by name on the JSON wire, that the zero mode
// stays omitted, and that both a name and an older client's number read back.
func Test_ForwardMode_JSONRoundTrip(t *testing.T) {
	encoded, err := json.Marshal(&forwardpb.Action{Target: "eth0", Mode: forwardpb.ForwardMode_OUT})
	require.NoError(t, err)
	require.JSONEq(t, `{"target": "eth0", "mode": "OUT"}`, string(encoded))

	zero, err := json.Marshal(&forwardpb.Action{Target: "eth0", Mode: forwardpb.ForwardMode_NONE})
	require.NoError(t, err)
	require.JSONEq(t, `{"target": "eth0"}`, string(zero))

	for _, input := range []string{`{"mode": "IN"}`, `{"mode": 1}`} {
		action := &forwardpb.Action{}
		require.NoError(t, json.Unmarshal([]byte(input), action), input)
		require.Equal(t, forwardpb.ForwardMode_IN, action.GetMode(), input)
	}
}

// Test_UpdateConfigRequest_DecodesYAMLRuleFile verifies that a rules file
// decodes its mode by name and its networks from bare CIDR strings.
func Test_UpdateConfigRequest_DecodesYAMLRuleFile(t *testing.T) {
	input := `
name: vlan-phy
rules:
  - action: {target: 0000:81:00.0, mode: OUT, counter: to_0000:81:00.0}
    devices: [{name: virtio_user_kni0}]
    vlan_ranges: [{from: 0, to: 4095}]
    sources4: [10.0.0.0/8]
    destinations6: [2001:db8::/32]
`
	request := &forwardpb.UpdateConfigRequest{}
	require.NoError(t, xproto.Unmarshal([]byte(input), request))

	source, err := xnetip.ParseNetwork4("10.0.0.0/8")
	require.NoError(t, err)
	destination, err := xnetip.ParseNetwork6("2001:db8::/32")
	require.NoError(t, err)
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
			Sources4:      []*commonpb.IPv4Network{commonpb.NewIPv4NetworkFrom4(source)},
			Destinations6: []*commonpb.IPv6Network{commonpb.NewIPv6NetworkFrom6(destination)},
		}},
	}
	require.True(t, proto.Equal(want, request), "got %v", request)
}
