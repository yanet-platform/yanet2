package operator_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// commandConfig exercises nested runtime decoding before resource construction.
type commandConfig struct {
	Readiness struct {
		RemoteNeighbourTable xcfg.NonEmptyString `yaml:"remote_neighbour_table"`
	} `yaml:"readiness"`
}

// Test_Run_ConfigOptions verifies that strict decoding reaches the command's
// loader, preserves environment overrides and remains opt-in for other users.
func Test_Run_ConfigOptions(t *testing.T) {
	for _, test := range []struct {
		name       string
		key        string
		strict     bool
		wantReject bool
	}{
		{name: "strict nested typo", key: "remote_neighbour_tabel", strict: true, wantReject: true},
		{name: "strict valid environment override", key: "remote_neighbour_table", strict: true},
		{name: "legacy permissive decoding", key: "remote_neighbour_tabel"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte("readiness:\n  "+test.key+": remote\n"), 0o600))
			t.Setenv("YANET_READINESS_REMOTE_NEIGHBOUR_TABLE", "from-env")
			original := os.Args
			os.Args = []string{"operator", "--config", path}
			t.Cleanup(func() { os.Args = original })
			constructed := false
			stop := errors.New("stop after loading config")
			factory := func(config *commandConfig, log *zap.Logger) (operator.Runnable, error) {
				constructed = true
				require.Equal(t, "from-env", config.Readiness.RemoteNeighbourTable.String())
				return nil, stop
			}
			var options []xcfg.Option
			if test.strict {
				options = append(options, xcfg.WithKnownFields())
			}
			err := operator.Run("operator", "configuration contract", factory, options...)
			if test.wantReject {
				require.ErrorContains(t, err, "remote_neighbour_tabel")
				require.False(t, constructed)
			} else {
				require.ErrorIs(t, err, stop)
				require.True(t, constructed)
			}
		})
	}
}
