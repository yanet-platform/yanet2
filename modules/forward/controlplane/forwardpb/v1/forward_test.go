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

// Test_ShowConfigRequest_Validate verifies that an empty or nil request is
// rejected while a named request passes.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *forwardpb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &forwardpb.ShowConfigRequest{}, message: "name is required"},
		{name: "name set", request: &forwardpb.ShowConfigRequest{Name: "forward0"}},
		{name: "nil request", request: nil, message: "name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_UpdateConfigRequest_Validate verifies that the request name is
// required and that nested rule errors include the repeated field path.
func Test_UpdateConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *forwardpb.UpdateConfigRequest
		message string
	}{
		{name: "empty name", request: &forwardpb.UpdateConfigRequest{}, message: "name is required"},
		{name: "name set", request: &forwardpb.UpdateConfigRequest{Name: "forward0"}},
		{
			name: "missing action at repeated index",
			request: &forwardpb.UpdateConfigRequest{
				Name: "forward0",
				Rules: []*forwardpb.Rule{
					{Action: &forwardpb.Action{}},
					{Action: &forwardpb.Action{}},
					{Action: &forwardpb.Action{}},
					{},
				},
			},
			message: "rules[3]: action is required",
		},
		{
			name: "nil rule at repeated index",
			request: &forwardpb.UpdateConfigRequest{
				Name:  "forward0",
				Rules: []*forwardpb.Rule{nil},
			},
			message: "rules[0]: action is required",
		},
		{
			name: "valid rule",
			request: &forwardpb.UpdateConfigRequest{
				Name:  "forward0",
				Rules: []*forwardpb.Rule{{Action: &forwardpb.Action{}}},
			},
		},
		{name: "nil request", request: nil, message: "name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_DeleteConfigRequest_Validate verifies that an empty or nil request is
// rejected while a named request passes.
func Test_DeleteConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *forwardpb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &forwardpb.DeleteConfigRequest{}, message: "name is required"},
		{name: "name set", request: &forwardpb.DeleteConfigRequest{Name: "forward0"}},
		{name: "nil request", request: nil, message: "name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_Rule_Validate verifies that an action is required and that a present
// action passes.
func Test_Rule_Validate(t *testing.T) {
	cases := []struct {
		name    string
		rule    *forwardpb.Rule
		message string
	}{
		{name: "missing action", rule: &forwardpb.Rule{}, message: "action is required"},
		{name: "action set", rule: &forwardpb.Rule{Action: &forwardpb.Action{}}},
		{name: "nil rule", rule: nil, message: "action is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rule.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

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
