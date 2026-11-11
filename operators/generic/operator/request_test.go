package operator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	decappb "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
	dscppb "github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
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

// Test_LoadRequest_IgnoresUnknownKeys verifies that newer module fields
// do not prevent an operator from publishing the fields it understands.
func Test_LoadRequest_IgnoresUnknownKeys(t *testing.T) {
	path := writeModuleConfig(t, `
name: dscp0
future: true
config:
  prefixes4: [192.0.2.0/24]
  future: {prefixes: [198.51.100.0/24]}
  dscp_config: {flag: 2, mark: 46, future: null}
`)
	request, err := operator.LoadRequest(
		"modules.dscp.controlplane.dscppb.v1.DscpService/UpdateConfig", path,
	)
	require.NoError(t, err)
	dscp := request.(*dscppb.UpdateConfigRequest)
	require.Equal(t, "dscp0", dscp.GetName())
	require.Len(t, dscp.GetConfig().GetPrefixes4(), 1)
	require.Equal(t, "192.0.2.0/24", dscp.GetConfig().GetPrefixes4()[0].AsLogValue())
	require.Equal(t, uint32(2), dscp.GetConfig().GetDscpConfig().GetFlag())
	require.Equal(t, uint32(46), dscp.GetConfig().GetDscpConfig().GetMark())
}
