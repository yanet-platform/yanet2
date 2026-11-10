package bird_test

import (
	"context"
	"encoding/binary"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

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
		func(context.Context) error { return nil },
		bird.WithExportReaderIdle(onIdle),
	)

	require.Error(t, export.Run(ctx))
	require.True(t, idle, "the idle callback did not run before the deadline")
}

// Test_Export_Run_SteadyStream verifies that routes arriving more often than
// the dump timeout are dumped long before the batch reaches its threshold.
func Test_Export_Run_SteadyStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bird.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)

	// Routes arrive twenty times per dump timeout, and the deadline leaves
	// room for fifty timeouts but not for a full batch.
	cfg := bird.DefaultConfig()
	cfg.Sockets = []string{path}
	cfg.DumpTimeout = 200 * time.Millisecond

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var server errgroup.Group
	server.Go(func() error {
		return streamUpdates(ctx, listener, 10*time.Millisecond)
	})

	dumped := 0
	onUpdate := func(_ context.Context, routes []rib.Route) error {
		dumped = len(routes)
		cancel()
		return nil
	}
	export := bird.NewExportReader(cfg, onUpdate, func(context.Context) error { return nil })

	require.Error(t, export.Run(ctx))
	require.NoError(t, listener.Close())
	require.NoError(t, server.Wait())
	require.NotZero(t, dumped, "no batch was dumped before the deadline")
	require.Less(t, dumped, cfg.DumpThreshold)
}

// Test_Export_Run_SocketFailureCancelsCallback verifies that a BIRD socket
// that hangs up cancels the context of an update callback still running, and
// that the reader then returns the socket failure rather than a cancellation.
func Test_Export_Run_SocketFailureCancelsCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bird.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })

	cfg := bird.DefaultConfig()
	cfg.Sockets = []string{path}
	cfg.DumpThreshold = 1

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// The socket hangs up only once the callback is waiting on its context.
	waiting := make(chan struct{})
	var server errgroup.Group
	server.Go(func() error {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		defer conn.Close()
		chunk := binary.LittleEndian.AppendUint32(nil, uint32(len(dataIPv6WithLargeCommunities)))
		chunk = append(chunk, dataIPv6WithLargeCommunities...)
		if _, err := conn.Write(chunk); err != nil {
			return err
		}
		select {
		case <-waiting:
		case <-ctx.Done():
		}
		return nil
	})

	onUpdate := func(callbackCtx context.Context, routes []rib.Route) error {
		close(waiting)
		select {
		case <-callbackCtx.Done():
			return context.Cause(callbackCtx)
		case <-ctx.Done():
			return nil
		}
	}
	export := bird.NewExportReader(cfg, onUpdate, func(context.Context) error { return nil })

	err = export.Run(ctx)
	require.NoError(t, server.Wait())
	require.Error(t, err)
	require.NoError(t, ctx.Err(), "the callback was not cancelled by the socket failure")
	require.NotErrorIs(t, err, context.Canceled)
}

// streamUpdates plays a BIRD export socket: it accepts one connection and
// writes it a valid route update every interval until the context ends.
func streamUpdates(ctx context.Context, listener net.Listener, interval time.Duration) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Each update goes out as a chunk prefixed with its size.
	chunk := binary.LittleEndian.AppendUint32(nil, uint32(len(dataIPv6WithLargeCommunities)))
	chunk = append(chunk, dataIPv6WithLargeCommunities...)

	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		if _, err := conn.Write(chunk); err != nil {
			// The reader hangs up once the context ends.
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}
