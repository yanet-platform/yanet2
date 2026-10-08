package operator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/pipeline/internal/operator"
)

const vxlanStageYAML = `gateways:
  - name: gw0
    endpoint: "[::1]:8080"
stages:
  - name: bootstrap
    pipelines:
      - name: main
        functions: []
    devices:
      vxlan:
        - name: vx0
          tunnel:
            local_ip: 192.0.2.1
            remote_ip: 198.51.100.7
            local_mac: "02:00:00:00:00:01"
            remote_mac: "02:00:00:00:00:02"
            vni: 4660
          input:
            - name: main
              weight: 2
          output:
            - name: main
              weight: 1
`

// Test_StageConfig_VxlanDevice_DecodesTunnel verifies that a vxlan device of
// a stage decodes its name, tunnel text addresses, VNI and pipeline
// bindings from the documented keys, with no key left unused.
func Test_StageConfig_VxlanDevice_DecodesTunnel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipeline.yaml")
	require.NoError(t, os.WriteFile(path, []byte(vxlanStageYAML), 0o600))
	require.NoError(t, xcfg.CheckKnownKeys[operator.Config]([]byte(vxlanStageYAML)))

	config, err := xcfg.LoadConfig[operator.Config](path)
	require.NoError(t, err)

	require.Len(t, config.Stages, 1)
	require.Equal(t, []operator.VXLANDeviceConfig{{
		Name: "vx0",
		Tunnel: operator.VXLANTunnelConfig{
			LocalIP:   "192.0.2.1",
			RemoteIP:  "198.51.100.7",
			LocalMAC:  "02:00:00:00:00:01",
			RemoteMAC: "02:00:00:00:00:02",
			VNI:       4660,
		},
		Input:  []operator.PipelineRefConfig{{Name: "main", Weight: 2}},
		Output: []operator.PipelineRefConfig{{Name: "main", Weight: 1}},
	}}, config.Stages[0].Devices.VXLAN)
}
