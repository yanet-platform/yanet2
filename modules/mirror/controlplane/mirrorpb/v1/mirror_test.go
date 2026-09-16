package mirrorpb_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	mirrorpb "github.com/yanet-platform/yanet2/modules/mirror/controlplane/mirrorpb/v1"
)

// Test_ShowConfigRequest_Validate verifies that an empty or nil request is
// rejected while a named request passes.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *mirrorpb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &mirrorpb.ShowConfigRequest{}, message: "name is required"},
		{name: "name set", request: &mirrorpb.ShowConfigRequest{Name: "mirror0"}},
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

// Test_UpdateConfigRequest_Validate verifies that names and rule structure
// are checked in order with repeated-field paths in nested errors.
func Test_UpdateConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *mirrorpb.UpdateConfigRequest
		message string
	}{
		{
			name:    "empty name",
			request: &mirrorpb.UpdateConfigRequest{},
			message: "name is required",
		},
		{
			name:    "nil rules",
			request: &mirrorpb.UpdateConfigRequest{Name: "mirror0"},
			message: "rules must contain at least one rule",
		},
		{
			name: "empty rules",
			request: &mirrorpb.UpdateConfigRequest{
				Name:  "mirror0",
				Rules: []*mirrorpb.Rule{},
			},
			message: "rules must contain at least one rule",
		},
		{
			name: "missing action at repeated index",
			request: &mirrorpb.UpdateConfigRequest{
				Name: "mirror0",
				Rules: []*mirrorpb.Rule{
					{Action: &mirrorpb.Action{}},
					{Action: &mirrorpb.Action{}},
					{Action: &mirrorpb.Action{}},
					{},
				},
			},
			message: "rules[3]: action is required",
		},
		{
			name: "nil rule at repeated index",
			request: &mirrorpb.UpdateConfigRequest{
				Name:  "mirror0",
				Rules: []*mirrorpb.Rule{nil},
			},
			message: "rules[0]: action is required",
		},
		{
			name: "valid rule",
			request: &mirrorpb.UpdateConfigRequest{
				Name:  "mirror0",
				Rules: []*mirrorpb.Rule{{Action: &mirrorpb.Action{}}},
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
		request *mirrorpb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &mirrorpb.DeleteConfigRequest{}, message: "name is required"},
		{name: "name set", request: &mirrorpb.DeleteConfigRequest{Name: "mirror0"}},
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

// Test_Rule_Validate verifies that a missing action is rejected, a present
// action passes, and a nil rule receiver returns the missing-action error.
//
// An invalid action is reported under the action field.
func Test_Rule_Validate(t *testing.T) {
	cases := []struct {
		name    string
		rule    *mirrorpb.Rule
		message string
	}{
		{name: "missing action", rule: &mirrorpb.Rule{}, message: "action is required"},
		{name: "action set", rule: &mirrorpb.Rule{Action: &mirrorpb.Action{}}},
		{
			name:    "invalid action",
			rule:    &mirrorpb.Rule{Action: &mirrorpb.Action{Mode: 3}},
			message: "action: mode unknown value 3",
		},
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

// Test_Action_Validate verifies that the target and counter obey the
// fixed-size buffer and NUL rules and that the mode is a declared value.
func Test_Action_Validate(t *testing.T) {
	cases := []struct {
		name    string
		action  *mirrorpb.Action
		message string
	}{
		{name: "empty action", action: &mirrorpb.Action{}},
		{
			name: "longest target and counter",
			action: &mirrorpb.Action{
				Target:  strings.Repeat("t", commonpb.MaxDeviceNameLen-1),
				Counter: strings.Repeat("c", commonpb.MaxCounterNameLen-1),
				Mode:    mirrorpb.MirrorMode_OUT,
			},
		},
		{
			name:    "target with NUL",
			action:  &mirrorpb.Action{Target: "eth0\x00eth1"},
			message: "target must not contain NUL",
		},
		{
			name:    "target of the buffer size",
			action:  &mirrorpb.Action{Target: strings.Repeat("t", commonpb.MaxDeviceNameLen)},
			message: "target must be shorter than 80 bytes",
		},
		{
			name:    "counter with NUL",
			action:  &mirrorpb.Action{Counter: "to_eth0\x00"},
			message: "counter must not contain NUL",
		},
		{
			name:    "counter of the buffer size",
			action:  &mirrorpb.Action{Counter: strings.Repeat("c", commonpb.MaxCounterNameLen)},
			message: "counter must be shorter than 128 bytes",
		},
		{
			name:    "undeclared mode",
			action:  &mirrorpb.Action{Mode: 3},
			message: "mode unknown value 3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.action.Validate()
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
func Test_MirrorMode_JSONRoundTrip(t *testing.T) {
	encoded, err := json.Marshal(&mirrorpb.Action{Target: "eth0", Mode: mirrorpb.MirrorMode_OUT})
	require.NoError(t, err)
	require.JSONEq(t, `{"target": "eth0", "mode": "OUT"}`, string(encoded))

	zero, err := json.Marshal(&mirrorpb.Action{Target: "eth0", Mode: mirrorpb.MirrorMode_NONE})
	require.NoError(t, err)
	require.JSONEq(t, `{"target": "eth0"}`, string(zero))

	for _, input := range []string{`{"mode": "IN"}`, `{"mode": 1}`} {
		action := &mirrorpb.Action{}
		require.NoError(t, json.Unmarshal([]byte(input), action), input)
		require.Equal(t, mirrorpb.MirrorMode_IN, action.GetMode(), input)
	}
}
