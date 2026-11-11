package debugserver_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/common/go/debugserver"
)

// Test_Server_Run_UnixSocket verifies that a unix socket endpoint replaces a
// stale socket left at its path and serves the profiles to its owner only.
//
// The socket mode is the only access control the endpoint has.
func Test_Server_Run_UnixSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.sock")

	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	stale.SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())

	server := debugserver.NewServer(&debugserver.Config{Endpoint: path})

	ctx, cancel := context.WithCancel(t.Context())
	group := errgroup.Group{}
	group.Go(func() error {
		return server.Run(ctx)
	})
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
		},
	}
	require.Eventually(t, func() bool {
		response, err := client.Get("http://debug/debug/pprof/")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, 5*time.Second, 10*time.Millisecond)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.ModeSocket|0o600, info.Mode())
}
