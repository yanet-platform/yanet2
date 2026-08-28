package mirrorpb_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	mirrorpb "github.com/yanet-platform/yanet2/modules/mirror/controlplane/mirrorpb/v1"
)

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
