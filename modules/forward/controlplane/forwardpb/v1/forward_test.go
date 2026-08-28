package forwardpb_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

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
