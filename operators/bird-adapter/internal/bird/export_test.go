package bird_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/test/bufconn"

	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/bird"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/rib"
)

// Test_Export_Run_IdleTick verifies that a reader whose socket stays silent
// runs the idle callback on its first idle tick, one dump timeout after the
// start by default.
func Test_Export_Run_IdleTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener, dial := listenBIRDExport(t)
		serveBIRDExport(t, listener, func(connection net.Conn) error {
			_, _ = io.Copy(io.Discard, connection)
			return nil
		})

		cfg := bird.DefaultConfig()
		cfg.Sockets = []string{"bird.sock"}
		cfg.DumpTimeout = time.Second

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		started := time.Now()
		var idleAfter time.Duration
		onIdle := func(context.Context) {
			idleAfter = time.Since(started)
			cancel()
		}
		export := bird.NewExportReader(
			cfg,
			func(context.Context, []rib.Route) error { return nil },
			func(context.Context) error { return nil },
			bird.WithExportReaderIdle(onIdle),
			dial,
		)

		require.Error(t, export.Run(ctx))
		require.Equal(t, cfg.DumpTimeout, idleAfter, "the idle callback did not run on the first tick")
	})
}

// Test_Export_Run_IdleIntervalApartFromDumpTimeout verifies that the idle
// callback runs every idle interval, however long the dump timeout, also
// while a route waits for its dump.
func Test_Export_Run_IdleIntervalApartFromDumpTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener, dial := listenBIRDExport(t)
		serveBIRDExport(t, listener, func(connection net.Conn) error {
			_, _ = connection.Write(updateChunk())
			_, _ = io.Copy(io.Discard, connection)
			return nil
		})

		cfg := bird.DefaultConfig()
		cfg.Sockets = []string{"bird.sock"}
		cfg.DumpTimeout = time.Hour
		cfg.DumpThreshold = 1000

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		started := time.Now()
		var idleAt []time.Duration
		onIdle := func(context.Context) {
			idleAt = append(idleAt, time.Since(started))
			if len(idleAt) == 3 {
				cancel()
			}
		}
		dumped := false
		export := bird.NewExportReader(
			cfg,
			func(context.Context, []rib.Route) error {
				dumped = true
				return nil
			},
			func(context.Context) error { return nil },
			bird.WithExportReaderIdle(onIdle),
			bird.WithExportReaderIdleInterval(time.Second),
			dial,
		)

		require.Error(t, export.Run(ctx))
		require.False(t, dumped, "the route must still wait for its dump")
		require.Equal(t, []time.Duration{time.Second, 2 * time.Second, 3 * time.Second}, idleAt)
	})
}

// Test_Export_Run_SteadyStream verifies that routes arriving more often than
// the dump timeout are dumped long before the batch reaches its threshold.
func Test_Export_Run_SteadyStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener, dial := listenBIRDExport(t)

		// Routes arrive twenty times per dump timeout, and the deadline
		// leaves room for fifty timeouts but not for a full batch.
		cfg := bird.DefaultConfig()
		cfg.Sockets = []string{"bird.sock"}
		cfg.DumpTimeout = 200 * time.Millisecond

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		serveBIRDExport(t, listener, func(connection net.Conn) error {
			return streamUpdates(ctx, connection, 10*time.Millisecond)
		})

		dumped := 0
		onUpdate := func(_ context.Context, routes []rib.Route) error {
			dumped = len(routes)
			cancel()
			return nil
		}
		export := bird.NewExportReader(cfg, onUpdate, func(context.Context) error { return nil }, dial)

		require.Error(t, export.Run(ctx))
		require.NotZero(t, dumped, "no batch was dumped before the deadline")
		require.Less(t, dumped, cfg.DumpThreshold)
	})
}

// Test_Export_Run_SocketFailureCancelsCallback verifies that a BIRD socket
// that hangs up cancels the context of an update callback still running, and
// that the reader then returns the socket failure rather than a cancellation.
func Test_Export_Run_SocketFailureCancelsCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener, dial := listenBIRDExport(t)

		cfg := bird.DefaultConfig()
		cfg.Sockets = []string{"bird.sock"}
		cfg.DumpThreshold = 1

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		// The socket hangs up only once the callback is waiting on its
		// context.
		waiting := make(chan struct{})
		serveBIRDExport(t, listener, func(connection net.Conn) error {
			if _, err := connection.Write(updateChunk()); err != nil {
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
		export := bird.NewExportReader(cfg, onUpdate, func(context.Context) error { return nil }, dial)

		err := export.Run(ctx)
		require.Error(t, err)
		require.NoError(t, ctx.Err(), "the callback was not cancelled by the socket failure")
		require.NotErrorIs(t, err, context.Canceled)
	})
}

// listenBIRDExport returns an in-memory BIRD export listener and the reader
// option that dials it for every socket path.
//
// Its connections block on condition variables, which a synctest bubble
// counts as durably blocked, unlike a socket.
func listenBIRDExport(t *testing.T) (*bufconn.Listener, bird.ExportReaderOption) {
	t.Helper()
	listener := bufconn.Listen(64 * 1024)
	dial := bird.WithExportReaderDialer(func(ctx context.Context, path string) (net.Conn, error) {
		return listener.DialContext(ctx)
	})
	return listener, dial
}

// serveBIRDExport serves every connection listener accepts with serve, and
// closes listener and the connections when the test ends.
func serveBIRDExport(t *testing.T, listener net.Listener, serve func(connection net.Conn) error) {
	t.Helper()
	var workers errgroup.Group
	workers.Go(func() error {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return nil
			}
			workers.Go(func() error {
				defer connection.Close()
				stop := context.AfterFunc(t.Context(), func() { _ = connection.Close() })
				defer stop()
				return serve(connection)
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		require.NoError(t, workers.Wait())
	})
}

// updateChunk returns a valid route update as BIRD sends it, prefixed with
// its size.
func updateChunk() []byte {
	chunk := binary.LittleEndian.AppendUint32(nil, uint32(len(dataIPv6WithLargeCommunities)))
	return append(chunk, dataIPv6WithLargeCommunities...)
}

// streamUpdates plays a BIRD export socket: it writes connection a valid
// route update every interval until the context ends.
func streamUpdates(ctx context.Context, connection net.Conn, interval time.Duration) error {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		if _, err := connection.Write(updateChunk()); err != nil {
			// The reader hangs up once the context ends.
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}
