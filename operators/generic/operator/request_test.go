package operator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	decappb "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
	"github.com/yanet-platform/yanet2/operators/generic/operator"
)

const decapUpdateMethod = "modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig"

// writeModuleConfig stores a module config file in a fresh directory and
// returns its path.
func writeModuleConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
	return path
}

// Test_LoadRequest_DecodesSpelledMethodRequest verifies that the file
// decodes into the request type the spelled method takes.
func Test_LoadRequest_DecodesSpelledMethodRequest(t *testing.T) {
	path := writeModuleConfig(t, `
name: decap0
prefixes4:
  - 10.0.0.0/8
`)

	request, err := operator.LoadRequest(decapUpdateMethod, path)
	require.NoError(t, err)

	decap, ok := request.(*decappb.UpdateConfigRequest)
	require.True(t, ok)
	require.Equal(t, "decap0", decap.GetName())
	require.Len(t, decap.GetPrefixes4(), 1)
	require.Equal(t, "10.0.0.0/8", decap.GetPrefixes4()[0].AsLogValue())
}

// Test_LoadRequest_RejectsUnknownKey verifies that a key outside the
// request is rejected, not ignored, naming the file.
func Test_LoadRequest_RejectsUnknownKey(t *testing.T) {
	path := writeModuleConfig(t, "config: decap0\nprefixes4: []\n")

	_, err := operator.LoadRequest(decapUpdateMethod, path)

	require.ErrorContains(t, err, "failed to parse module config")
	require.ErrorContains(t, err, `unknown field "config"`)
}
