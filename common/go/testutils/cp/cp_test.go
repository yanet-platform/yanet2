package cp_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/testutils/cp"
)

const (
	smokeCP = 8 * 1024 * 1024
	smokeDP = 4 * 1024 * 1024
)

// TestSHM_Lifecycle verifies that the Go wrapper correctly creates a SHM
// arena and closes it without error. No modules are registered because the
// cp package is generic and does not link any module dataplane library.
func TestSHM_Lifecycle(t *testing.T) {
	shm, err := cp.NewSHM(smokeCP, smokeDP, nil)
	require.NoError(t, err)
	require.NotNil(t, shm)
	require.NotNil(t, shm.RawPtr())

	err = shm.Close()
	require.NoError(t, err)
}
