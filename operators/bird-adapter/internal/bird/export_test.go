package bird_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/bird"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/rib"
)

// Test_Export_Run_IdleTick verifies that a reader whose socket stays silent
// runs the idle callback on its dump ticks.
func Test_Export_Run_IdleTick(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bird.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })

	cfg := bird.DefaultConfig()
	cfg.Sockets = []string{path}
	cfg.DumpTimeout = time.Millisecond

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	idle := false
	onIdle := func(context.Context) {
		idle = true
		cancel()
	}
	export := bird.NewExportReader(
		cfg,
		func(context.Context, []rib.Route) error { return nil },
		func() error { return nil },
		bird.WithExportReaderIdle(onIdle),
	)

	require.Error(t, export.Run(ctx))
	require.True(t, idle, "the idle callback did not run before the deadline")
}
