package operator_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
)

// Test_Config_RuntimeLoader verifies that a misspelled remote gate cannot
// construct an operator with local monitoring disabled, including env overlays.
func Test_Config_RuntimeLoader(t *testing.T) {
	for _, key := range []string{"remote_neighbour_table", "remote_neighbour_tabel"} {
		t.Run(key, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "route.yaml")
			data := "gateways:\n- name: first\n  endpoint: 127.0.0.1:8080\n" +
				"netlink_monitor:\n  disabled: true\nreadiness:\n  " + key + ": remote\n" +
				"gateway_devices:\n  first: [logical0]\n"
			require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
			t.Setenv("YANET_READINESS_REMOTE_NEIGHBOUR_TABLE", "from-env")
			stop := errors.New("validated configuration")
			constructed := false
			err := commonoperator.RunOperator(path, func(config *operator.Config, log *zap.Logger) (commonoperator.Runnable, error) {
				constructed = true
				require.Equal(t, "from-env", config.Readiness.RemoteNeighbourTable)
				require.True(t, config.NetlinkMonitor.Disabled)
				return nil, stop
			}, xcfg.WithKnownFields())
			if key == "remote_neighbour_table" {
				require.ErrorIs(t, err, stop)
			} else {
				require.ErrorContains(t, err, key)
				require.False(t, constructed)
			}
		})
	}
}
