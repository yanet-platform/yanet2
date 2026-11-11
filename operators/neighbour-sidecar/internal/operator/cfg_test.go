package operator_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/neighbour-sidecar/internal/operator"
)

// Test_Config_LoadRejectsOverlongLinkMapDevice verifies that an explicit
// mapped device at the native byte limit fails configuration loading.
func Test_Config_LoadRejectsOverlongLinkMapDevice(t *testing.T) {
	device := strings.Repeat("a", commonpb.MaxDeviceNameLen)
	data := fmt.Sprintf(`gateways:
  - name: numa0
    endpoint: "[::1]:8080"
link_map:
  eth0: %q
`, device)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	_, err := xcfg.LoadConfig[operator.Config](path)
	require.EqualError(
		t,
		err,
		`failed to parse config file: link_map["eth0"] must be shorter than 80 bytes`,
	)
}
