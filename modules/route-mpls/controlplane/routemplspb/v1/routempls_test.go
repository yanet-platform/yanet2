package routemplspb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	routemplspb "github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/routemplspb/v1"
)

// Test_CreateConfigRequest_Validate verifies that a config name is required
// and that nested next-hop labels retain their repeated-field path.
func Test_CreateConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *routemplspb.CreateConfigRequest
		message string
	}{
		{
			name:    "empty name",
			request: &routemplspb.CreateConfigRequest{},
			message: "name is required",
		},
		{
			name: "label above maximum",
			request: &routemplspb.CreateConfigRequest{
				Name: "mpls0",
				Rules: []*routemplspb.Rule{{
					Nexthop: &routemplspb.NextHop{Label: 1048576},
				}},
			},
			message: "rules[0]: nexthop: label 1048576 must be in range 0..1048575",
		},
		{
			name: "maximum label",
			request: &routemplspb.CreateConfigRequest{
				Name: "mpls0",
				Rules: []*routemplspb.Rule{{
					Nexthop: &routemplspb.NextHop{Label: 1048575},
				}},
			},
		},
		{
			name: "parser-owned fields omitted",
			request: &routemplspb.CreateConfigRequest{
				Name:  "mpls0",
				Rules: []*routemplspb.Rule{{}},
			},
		},
		{name: "nil request", message: "name is required"},
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

// Test_UpdateConfigRequest_Validate verifies that names and both update event
// variants delegate label validation with their complete field paths.
func Test_UpdateConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *routemplspb.UpdateConfigRequest
		message string
	}{
		{
			name:    "empty name",
			request: &routemplspb.UpdateConfigRequest{},
			message: "name is required",
		},
		{
			name: "update label above maximum",
			request: &routemplspb.UpdateConfigRequest{
				Name: "mpls0",
				Updates: []*routemplspb.UpdateEvent{{
					Event: &routemplspb.UpdateEvent_Update{
						Update: &routemplspb.Rule{
							Nexthop: &routemplspb.NextHop{Label: 1048576},
						},
					},
				}},
			},
			message: "updates[0]: update: nexthop: label 1048576 must be in range 0..1048575",
		},
		{
			name: "withdraw label above maximum",
			request: &routemplspb.UpdateConfigRequest{
				Name: "mpls0",
				Updates: []*routemplspb.UpdateEvent{{
					Event: &routemplspb.UpdateEvent_Withdraw{
						Withdraw: &routemplspb.Rule{
							Nexthop: &routemplspb.NextHop{Label: 1048576},
						},
					},
				}},
			},
			message: "updates[0]: withdraw: nexthop: label 1048576 must be in range 0..1048575",
		},
		{
			name: "parser-owned fields omitted",
			request: &routemplspb.UpdateConfigRequest{
				Name: "mpls0",
				Updates: []*routemplspb.UpdateEvent{{
					Event: &routemplspb.UpdateEvent_Update{
						Update: &routemplspb.Rule{},
					},
				}},
			},
		},
		{
			name:    "empty update list",
			request: &routemplspb.UpdateConfigRequest{Name: "mpls0"},
		},
		{name: "nil request", message: "name is required"},
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

// Test_ShowConfigRequest_Validate verifies that an empty or nil request is
// rejected while a named request passes.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *routemplspb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &routemplspb.ShowConfigRequest{}, message: "name is required"},
		{name: "name set", request: &routemplspb.ShowConfigRequest{Name: "mpls0"}},
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
		request *routemplspb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &routemplspb.DeleteConfigRequest{}, message: "name is required"},
		{name: "name set", request: &routemplspb.DeleteConfigRequest{Name: "mpls0"}},
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

// Test_UpdateEvent_Validate verifies that each oneof branch reports invalid
// labels without rejecting an event whose parser-owned fields are absent.
func Test_UpdateEvent_Validate(t *testing.T) {
	cases := []struct {
		name    string
		event   *routemplspb.UpdateEvent
		message string
	}{
		{
			name: "update label above maximum",
			event: &routemplspb.UpdateEvent{Event: &routemplspb.UpdateEvent_Update{
				Update: &routemplspb.Rule{Nexthop: &routemplspb.NextHop{Label: 1048576}},
			}},
			message: "update: nexthop: label 1048576 must be in range 0..1048575",
		},
		{
			name: "withdraw label above maximum",
			event: &routemplspb.UpdateEvent{Event: &routemplspb.UpdateEvent_Withdraw{
				Withdraw: &routemplspb.Rule{Nexthop: &routemplspb.NextHop{Label: 1048576}},
			}},
			message: "withdraw: nexthop: label 1048576 must be in range 0..1048575",
		},
		{name: "event without a branch", event: &routemplspb.UpdateEvent{}},
		{name: "nil event"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.event.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_Rule_Validate verifies that a present next hop validates its label
// while parser-owned prefix and address fields remain outside validation.
func Test_Rule_Validate(t *testing.T) {
	cases := []struct {
		name    string
		rule    *routemplspb.Rule
		message string
	}{
		{name: "missing next hop", rule: &routemplspb.Rule{}},
		{name: "label at maximum", rule: &routemplspb.Rule{Nexthop: &routemplspb.NextHop{Label: 1048575}}},
		{
			name:    "label above maximum",
			rule:    &routemplspb.Rule{Nexthop: &routemplspb.NextHop{Label: 1048576}},
			message: "nexthop: label 1048576 must be in range 0..1048575",
		},
		{name: "nil rule"},
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

// Test_NextHop_Validate verifies that the 20-bit label boundary is inclusive
// and that a label above it is rejected.
func Test_NextHop_Validate(t *testing.T) {
	cases := []struct {
		name    string
		nexthop *routemplspb.NextHop
		message string
	}{
		{name: "zero label", nexthop: &routemplspb.NextHop{}},
		{name: "maximum label", nexthop: &routemplspb.NextHop{Label: 1048575}},
		{
			name:    "label above maximum",
			nexthop: &routemplspb.NextHop{Label: 1048576},
			message: "label 1048576 must be in range 0..1048575",
		},
		{name: "nil next hop"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.nexthop.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}
