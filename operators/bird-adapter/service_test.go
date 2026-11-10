package bird_adapter_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	birdadapter "github.com/yanet-platform/yanet2/operators/bird-adapter"
	adapterpb "github.com/yanet-platform/yanet2/operators/bird-adapter/adapterpb/v1"
	routepb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// Test_AdapterService_SetupConfig_Codes verifies that SetupConfig reports
// unusable import parameters as InvalidArgument and an unreachable route
// operator as Unavailable.
func Test_AdapterService_SetupConfig_Codes(t *testing.T) {
	sourceV4, err := commonpb.NewIPv4AddressFromAddr(netip.MustParseAddr("192.0.2.1"))
	require.NoError(t, err)
	sourceV6, err := commonpb.NewIPv6AddressFromAddr(netip.MustParseAddr("2001:db8::1"))
	require.NoError(t, err)
	mappedV6 := commonpb.NewIPv6Address(netip.MustParseAddr("::ffff:192.0.2.1").As16())

	cases := []struct {
		name     string
		sockets  []string
		sourceV6 *commonpb.IPv6Address
		code     codes.Code
	}{
		{
			name:     "IPv4-mapped v6 source",
			sockets:  []string{filepath.Join(t.TempDir(), "bird.sock")},
			sourceV6: mappedV6,
			code:     codes.InvalidArgument,
		},
		{
			name:     "unreachable route operator",
			sockets:  []string{filepath.Join(t.TempDir(), "bird.sock")},
			sourceV6: sourceV6,
			code:     codes.Unavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := "unix://" + filepath.Join(t.TempDir(), "route-operator.sock")
			svc := birdadapter.NewAdapterService(endpoint)

			_, err := svc.SetupConfig(t.Context(), &adapterpb.SetupConfigRequest{
				Name:     "bird0",
				Config:   &adapterpb.ImportConfig{Sockets: tc.sockets},
				SourceV4: sourceV4,
				SourceV6: tc.sourceV6,
			})
			require.Equal(t, tc.code, status.Code(err))
		})
	}
}

// Test_AdapterService_IdleImportReplaysAfterGatewayRestart verifies that a quiet
// import reopens its application stream and replays its RIB after transport loss.
func Test_AdapterService_IdleImportReplaysAfterGatewayRestart(t *testing.T) {
	cases := []struct {
		name    string
		reapply bool
	}{
		{name: "automatic recovery without BIRD updates"},
		{name: "reapplying the same import restores the RIB", reapply: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			socket := startIdleBIRDExport(t)
			receiver := &recordingRIBReceiver{Committed: make(chan []*routepb.Update, 4)}
			endpoint := startRIBReceiver(t, "127.0.0.1:0", receiver)
			service := birdadapter.NewAdapterService(endpoint.Address)
			sourceV4, err := commonpb.NewIPv4AddressFromAddr(netip.MustParseAddr("192.0.2.1"))
			require.NoError(t, err)
			sourceV6, err := commonpb.NewIPv6AddressFromAddr(netip.MustParseAddr("2001:db8::1"))
			require.NoError(t, err)
			request := &adapterpb.SetupConfigRequest{
				Name: "bird0",
				Config: &adapterpb.ImportConfig{
					Sockets:       []string{socket},
					DumpThreshold: 1,
				},
				SourceV4: sourceV4,
				SourceV6: sourceV6,
			}
			defer func() {
				// Replacing the import stops its detached reader; the empty
				// replacement exits immediately without an export socket.
				cleanup := proto.Clone(request).(*adapterpb.SetupConfigRequest)
				cleanup.Config.Sockets = nil
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				_, err := service.SetupConfig(ctx, cleanup)
				require.NoError(t, err)
			}()
			_, err = service.SetupConfig(t.Context(), request)
			require.NoError(t, err)
			initial := receiveRIBCommit(t, receiver.Committed)
			require.Len(t, initial, 2)
			require.NotNil(t, initial[0].GetRoute())
			require.Nil(t, initial[1].GetRoute())

			endpoint.Server.Stop()
			startRIBReceiver(t, endpoint.Address, receiver)
			connection, err := grpc.NewClient(endpoint.Address,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = connection.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, err = routepb.NewRouteServiceClient(connection).ListConfigs(ctx,
				&routepb.ListConfigsRequest{}, grpc.WaitForReady(true),
			)
			require.NoError(t, err, "the replacement endpoint must already be reachable")
			if tc.reapply {
				_, err = service.SetupConfig(t.Context(), request)
				require.NoError(t, err)
			}

			replayed := receiveRIBCommit(t, receiver.Committed)
			require.Len(t, replayed, len(initial))
			for idx := range initial {
				require.True(t, proto.Equal(initial[idx], replayed[idx]),
					"the replacement stream must receive the same route and flush",
				)
			}
		})
	}
}

type ribReceiverEndpoint struct {
	Address string
	Server  *grpc.Server
}

// startRIBReceiver serves a local RIB endpoint which can be stopped and rebound.
func startRIBReceiver(t *testing.T, address string, receiver *recordingRIBReceiver) ribReceiverEndpoint {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	require.NoError(t, err)
	server := grpc.NewServer()
	routepb.RegisterRouteServiceServer(server, receiver)
	var workers errgroup.Group
	workers.Go(func() error { return server.Serve(listener) })
	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, workers.Wait())
	})
	return ribReceiverEndpoint{Address: listener.Addr().String(), Server: server}
}

type recordingRIBReceiver struct {
	routepb.UnimplementedRouteServiceServer
	Committed chan []*routepb.Update
}

func (m *recordingRIBReceiver) FeedRIB(stream grpc.ClientStreamingServer[routepb.Update, routepb.UpdateSummary]) error {
	var updates []*routepb.Update
	for {
		update, err := stream.Recv()
		if err != nil {
			return err
		}
		if update.GetHeartbeat() {
			continue
		}
		updates = append(updates, update)
		if update.GetRoute() == nil {
			select {
			case m.Committed <- updates:
				updates = nil
			case <-stream.Context().Done():
				return stream.Context().Err()
			}
		}
	}
}

func (m *recordingRIBReceiver) ListConfigs(ctx context.Context, request *routepb.ListConfigsRequest) (*routepb.ListConfigsResponse, error) {
	return &routepb.ListConfigsResponse{}, nil
}

// receiveRIBCommit waits for application data, not just transport connectivity.
func receiveRIBCommit(t *testing.T, commits <-chan []*routepb.Update) []*routepb.Update {
	t.Helper()
	select {
	case updates := <-commits:
		return updates
	case <-time.After(5 * time.Second):
		t.Fatal("no RIB commit: the idle import did not open and populate a new FeedRIB stream")
		return nil
	}
}

// startIdleBIRDExport dumps one unchanged route per connection, then stays quiet.
func startIdleBIRDExport(t *testing.T) string {
	t.Helper()
	// Use a short path because Unix socket names have a platform length limit.
	directory, err := os.MkdirTemp("", "bird-export-")
	require.NoError(t, err)
	socket := filepath.Join(directory, "export.sock")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
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
				update := []byte{
					0: 92,
					4: 2, 5: 48, 6: 20,
					8: 0xb8, 9: 0x0d, 10: 1, 11: 0x20,
					44: 1,
					68: 24,
					72: 3, 73: 4, 76: 16,
					80: 0xb8, 81: 0x0d, 82: 1, 83: 0x20,
					92: 1, 95: 0,
				}
				binary.LittleEndian.PutUint32(update[:4], uint32(len(update)-4))
				if _, err := connection.Write(update); err != nil {
					return err
				}
				_, _ = io.Copy(io.Discard, connection)
				return nil
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		require.NoError(t, workers.Wait())
		require.NoError(t, os.RemoveAll(directory))
	})
	return socket
}

// Test_AdapterService_BusyImportReopensStreamAfterGatewayRestart verifies
// that an import whose BIRD keeps sending routes opens a new stream once the
// route operator endpoint comes back.
func Test_AdapterService_BusyImportReopensStreamAfterGatewayRestart(t *testing.T) {
	socket := startBIRDExport(t, func(idx int, connection net.Conn) error {
		for {
			if _, err := connection.Write(birdRouteChunk(1)); err != nil {
				return nil
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	opened := make(chan int, 16)
	recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
		first := true
		for {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			if first {
				first = false
				opened <- idx
			}
		}
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := serveRouteService(t, listener, recorder)
	startImport(t, listener.Addr().String(), socket)

	require.Equal(t, 0, receiveOpened(t, opened))
	server.Stop()
	listener, err = net.Listen("tcp", listener.Addr().String())
	require.NoError(t, err)
	serveRouteService(t, listener, recorder)

	require.Equal(t, 1, receiveOpened(t, opened))
}

// Test_AdapterService_ServerClosedStreamRestarts verifies that an idle import
// whose stream the route operator ends with a summary opens a new stream and
// replays its table on it.
func Test_AdapterService_ServerClosedStreamRestarts(t *testing.T) {
	socket := startQuietBIRDExport(t)
	commits := make(chan []*routepb.Update, 4)
	recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
		var updates []*routepb.Update
		for {
			update, err := stream.Recv()
			if err != nil {
				return err
			}
			updates = append(updates, update)
			if update.GetRoute() != nil || update.GetHeartbeat() {
				continue
			}
			if idx == 0 {
				return stream.SendAndClose(&routepb.UpdateSummary{})
			}
			commits <- updates
			updates = nil
		}
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serveRouteService(t, listener, recorder)
	startImport(t, listener.Addr().String(), socket)

	replayed := receiveRIBCommit(t, commits)
	require.Len(t, replayed, 2)
	require.NotNil(t, replayed[0].GetRoute())
	require.Nil(t, replayed[1].GetRoute())
}

// Test_AdapterService_Close_DuringRetryBackoff verifies that closing the
// service stops an import that the route operator keeps rejecting at once,
// without waiting out its retry delay.
func Test_AdapterService_Close_DuringRetryBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := newMemoryNetwork()
		serveBIRDExport(t, network.Listen("bird.sock"), quietBIRDExport)
		opened := make(chan time.Time, 64)
		serveRouteService(t, network.Listen(memoryRouteOperator), rejectingRecorder(opened))
		service := startMemoryImport(t, network, newImportRequest(t, "bird.sock"))

		// The delay before the sixteenth stream is at least 15 s.
		for range 15 {
			receiveEvent(t, opened)
		}
		synctest.Wait()

		closing := time.Now()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		require.NoError(t, service.Close(ctx))
		require.Zero(t, time.Since(closing))
	})
}

// Test_AdapterService_RetryDelay_CappedAfterJitter verifies that no wait
// between attempts the route operator rejects at once exceeds 30 s, the
// jitter included, and that the waits do reach that cap.
func Test_AdapterService_RetryDelay_CappedAfterJitter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := newMemoryNetwork()
		serveBIRDExport(t, network.Listen("bird.sock"), quietBIRDExport)
		opened := make(chan time.Time, 64)
		serveRouteService(t, network.Listen(memoryRouteOperator), rejectingRecorder(opened))
		startMemoryImport(t, network, newImportRequest(t, "bird.sock"))

		// The delay grows past the cap after about ten attempts; half of
		// the later ones would overshoot it without the cap.
		previous := receiveEvent(t, opened)
		capped := 0
		for range 40 {
			next := receiveEvent(t, opened)
			delay := next.Sub(previous)
			require.LessOrEqual(t, delay, 30*time.Second)
			if delay == 30*time.Second {
				capped++
			}
			previous = next
		}
		require.NotZero(t, capped, "no retry delay reached the cap")
	})
}

// Test_AdapterService_RetryDelay_ResetAfterHealthyAttempt verifies that an
// attempt that stayed up for ten minutes makes the next retry wait the
// initial delay again, and that a shorter one keeps the grown delay.
func Test_AdapterService_RetryDelay_ResetAfterHealthyAttempt(t *testing.T) {
	cases := []struct {
		name   string
		uptime time.Duration
		// check asserts the wait between the end of the long attempt and
		// the next stream.
		check func(t *testing.T, delay time.Duration)
	}{
		{
			name:   "ten minutes up restarts from the initial delay",
			uptime: 10 * time.Minute,
			check: func(t *testing.T, delay time.Duration) {
				// The initial 500 ms delay with its 50 % jitter.
				require.LessOrEqual(t, delay, 750*time.Millisecond)
			},
		},
		{
			name:   "less than ten minutes up keeps the capped delay",
			uptime: 10*time.Minute - time.Second,
			check: func(t *testing.T, delay time.Duration) {
				// The 30 s cap with its 50 % jitter.
				require.GreaterOrEqual(t, delay, 15*time.Second)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Twenty rejected streams grow the delay to its cap, then
				// the route operator keeps one stream for the uptime.
				const longAttempt = 20
				network := newMemoryNetwork()
				serveBIRDExport(t, network.Listen("bird.sock"), quietBIRDExport)
				opened := make(chan time.Time, 64)
				recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
					select {
					case opened <- time.Now():
					default:
					}
					if idx == longAttempt {
						select {
						case <-time.After(tc.uptime):
						case <-stream.Context().Done():
						}
					}
					return status.Error(codes.Unavailable, "route operator rejects the stream")
				}}
				serveRouteService(t, network.Listen(memoryRouteOperator), recorder)
				startMemoryImport(t, network, newImportRequest(t, "bird.sock"))

				var long time.Time
				for range longAttempt + 1 {
					long = receiveEvent(t, opened)
				}
				next := receiveEvent(t, opened)
				tc.check(t, next.Sub(long.Add(tc.uptime)))
			})
		})
	}
}

// Test_AdapterService_BIRDSocketFailureReleasesBlockedSend verifies that a
// BIRD socket hanging up while an update waits on a stream blocked by flow
// control ends the attempt, and the import opens a new stream.
//
// One socket floods the import until its send blocks; the other one, still
// read, hangs up then.
func Test_AdapterService_BIRDSocketFailureReleasesBlockedSend(t *testing.T) {
	stalled := make(chan struct{})
	floodSocket := startBIRDExport(t, func(idx int, connection net.Conn) error {
		if idx == 0 {
			floodBIRDExport(connection)
			close(stalled)
		} else {
			_, _ = connection.Write(birdRouteChunk(1))
		}
		_, _ = io.Copy(io.Discard, connection)
		return nil
	})
	hangupSocket := startBIRDExport(t, func(idx int, connection net.Conn) error {
		if idx == 0 {
			<-stalled
			return nil
		}
		_, _ = connection.Write(birdRouteChunk(2))
		_, _ = io.Copy(io.Discard, connection)
		return nil
	})
	recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
		<-stream.Context().Done()
		return stream.Context().Err()
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serveRouteService(t, listener, recorder, fixedFlowControlWindows()...)
	startImport(t, listener.Addr().String(), floodSocket, hangupSocket)

	require.Eventually(t, func() bool { return len(recorder.Streams()) >= 2 },
		10*time.Second, 10*time.Millisecond,
		"the import did not open a new stream after its BIRD socket failed",
	)
}

// Test_AdapterService_SetupConfig_ReplacementLeavesOneWriter verifies that
// replacing an import ends the stream of the replaced one, so that only the
// new import keeps writing the RIB.
func Test_AdapterService_SetupConfig_ReplacementLeavesOneWriter(t *testing.T) {
	firstSocket := startQuietBIRDExport(t)
	secondSocket := startQuietBIRDExport(t)
	commits := make(chan int, 4)
	recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
		for {
			update, err := stream.Recv()
			if err != nil {
				return err
			}
			if update.GetRoute() == nil && !update.GetHeartbeat() {
				commits <- idx
			}
		}
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serveRouteService(t, listener, recorder)
	service := startImport(t, listener.Addr().String(), firstSocket)
	require.Equal(t, 0, receiveOpened(t, commits))

	_, err = service.SetupConfig(t.Context(), newImportRequest(t, secondSocket))
	require.NoError(t, err)

	require.Equal(t, 1, receiveOpened(t, commits))
	streams := recorder.Streams()
	require.Len(t, streams, 2)
	require.Eventually(t, func() bool { return streams[0].Err() != nil },
		5*time.Second, 10*time.Millisecond,
		"the replaced import kept its stream open",
	)
	require.NoError(t, streams[1].Err())
	sessions, err := service.ListSessions(t.Context(), &adapterpb.ListSessionsRequest{})
	require.NoError(t, err)
	require.Len(t, sessions.GetSessions(), 1)
	require.Equal(t, []string{secondSocket}, sessions.GetSessions()[0].GetSockets())
}

// Test_AdapterService_SetupConfig_FailedReplacementKeepsImport verifies that
// a replacement that cannot reach the route operator reports Unavailable and
// leaves the running import and its stream in place.
func Test_AdapterService_SetupConfig_FailedReplacementKeepsImport(t *testing.T) {
	socket := startQuietBIRDExport(t)
	commits := make(chan int, 4)
	recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
		for {
			update, err := stream.Recv()
			if err != nil {
				return err
			}
			if update.GetRoute() == nil && !update.GetHeartbeat() {
				commits <- idx
			}
		}
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	gated := &gatedListener{Listener: listener}
	serveRouteService(t, gated, recorder)
	service := startImport(t, listener.Addr().String(), socket)
	require.Equal(t, 0, receiveOpened(t, commits))
	before, err := service.ListSessions(t.Context(), &adapterpb.ListSessionsRequest{})
	require.NoError(t, err)

	gated.Refuse.Store(true)
	_, err = service.SetupConfig(t.Context(), newImportRequest(t, socket))
	require.Equal(t, codes.Unavailable, status.Code(err))

	streams := recorder.Streams()
	require.Len(t, streams, 1)
	require.NoError(t, streams[0].Err(), "the failed replacement ended the running stream")
	after, err := service.ListSessions(t.Context(), &adapterpb.ListSessionsRequest{})
	require.NoError(t, err)
	require.Len(t, after.GetSessions(), 1)
	require.Equal(t, before.GetSessions()[0].GetCreatedAt(), after.GetSessions()[0].GetCreatedAt())
}

// Test_AdapterService_Close_DuringSetup verifies that closing the service
// cancels a setup still waiting for the route operator within the close
// deadline, and that the closed service refuses new setups.
func Test_AdapterService_Close_DuringSetup(t *testing.T) {
	socket := startQuietBIRDExport(t)
	// The endpoint accepts connections but never speaks gRPC, so the setup
	// waits for the connection to become ready.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	accepted := make(chan net.Conn, 4)
	var acceptor errgroup.Group
	acceptor.Go(func() error {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return nil
			}
			accepted <- connection
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		require.NoError(t, acceptor.Wait())
		close(accepted)
		for connection := range accepted {
			_ = connection.Close()
		}
	})
	service := birdadapter.NewAdapterService(listener.Addr().String())

	var setup errgroup.Group
	setup.Go(func() error {
		_, err := service.SetupConfig(t.Context(), newImportRequest(t, socket))
		return err
	})
	select {
	case connection := <-accepted:
		accepted <- connection
	case <-time.After(5 * time.Second):
		t.Fatal("the setup did not connect to the route operator endpoint")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	require.NoError(t, service.Close(ctx))
	require.Error(t, setup.Wait())
	_, err = service.SetupConfig(t.Context(), newImportRequest(t, socket))
	require.Error(t, err, "a closed service accepted a new setup")
}

// Test_AdapterService_Close_WithBlockedSend verifies that closing the service
// stops an import whose update waits on a stream blocked by flow control
// within the close deadline.
func Test_AdapterService_Close_WithBlockedSend(t *testing.T) {
	stalled := make(chan struct{})
	socket := startBIRDExport(t, func(idx int, connection net.Conn) error {
		floodBIRDExport(connection)
		close(stalled)
		_, _ = io.Copy(io.Discard, connection)
		return nil
	})
	recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
		<-stream.Context().Done()
		return stream.Context().Err()
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serveRouteService(t, listener, recorder, fixedFlowControlWindows()...)
	service := birdadapter.NewAdapterService(listener.Addr().String())
	_, err = service.SetupConfig(t.Context(), newImportRequest(t, socket))
	require.NoError(t, err)

	select {
	case <-stalled:
	case <-time.After(10 * time.Second):
		t.Fatal("the import never stopped reading its BIRD socket")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	require.NoError(t, service.Close(ctx))
}

// Test_AdapterService_SetupConfig_NoSocketsStartsNoStream verifies that an
// import without sockets opens no stream and stops at once instead of
// retrying.
func Test_AdapterService_SetupConfig_NoSocketsStartsNoStream(t *testing.T) {
	recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
		<-stream.Context().Done()
		return stream.Context().Err()
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serveRouteService(t, listener, recorder)
	service := startImport(t, listener.Addr().String())

	require.Eventually(t, func() bool {
		sessions, err := service.ListSessions(t.Context(), &adapterpb.ListSessionsRequest{})
		require.NoError(t, err)
		require.Len(t, sessions.GetSessions(), 1)
		return sessions.GetSessions()[0].GetConnectionState() == adapterpb.ConnectionState_CONNECTION_STATE_SHUTDOWN
	}, 5*time.Second, 10*time.Millisecond)
	require.Empty(t, recorder.Streams())
}

// Test_AdapterService_IdleImportSendsHeartbeats verifies that an import
// whose BIRD stays quiet after its table sends one named heartbeat per dump
// timeout on its stream.
func Test_AdapterService_IdleImportSendsHeartbeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const dumpTimeout = 7 * time.Second
		network := newMemoryNetwork()
		serveBIRDExport(t, network.Listen("bird.sock"), quietBIRDExport)
		committed := make(chan time.Time, 1)
		heartbeats := make(chan receivedUpdate, 16)
		recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
			for {
				update, err := stream.Recv()
				if err != nil {
					return err
				}
				switch {
				case update.GetHeartbeat():
					select {
					case heartbeats <- receivedUpdate{At: time.Now(), Update: update}:
					default:
					}
				case update.GetRoute() == nil:
					select {
					case committed <- time.Now():
					default:
					}
				}
			}
		}}
		serveRouteService(t, network.Listen(memoryRouteOperator), recorder)
		request := newImportRequest(t, "bird.sock")
		request.Config.DumpTimeout = int64(dumpTimeout)
		startMemoryImport(t, network, request)

		start := receiveEvent(t, committed)
		for idx := range 3 {
			heartbeat := receiveEvent(t, heartbeats)
			require.Equal(t, time.Duration(idx+1)*dumpTimeout, heartbeat.At.Sub(start))
			require.Equal(t, "bird0", heartbeat.Update.GetName())
			require.Nil(t, heartbeat.Update.GetRoute())
		}
	})
}

// Test_AdapterService_FailedHeartbeatReplaysOnNewStream verifies that a
// stream failing on a heartbeat makes an idle import replay its table on a
// new stream.
func Test_AdapterService_FailedHeartbeatReplaysOnNewStream(t *testing.T) {
	socket := startQuietBIRDExport(t)
	commits := make(chan []*routepb.Update, 4)
	recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
		var updates []*routepb.Update
		for {
			update, err := stream.Recv()
			if err != nil {
				return err
			}
			if update.GetHeartbeat() {
				if idx == 0 {
					return status.Error(codes.Unavailable, "the stream died")
				}
				continue
			}
			updates = append(updates, update)
			if update.GetRoute() == nil && idx > 0 {
				commits <- updates
				updates = nil
			}
		}
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serveRouteService(t, listener, recorder)
	request := newImportRequest(t, socket)
	request.Config.DumpTimeout = int64(20 * time.Millisecond)
	service := birdadapter.NewAdapterService(listener.Addr().String())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		require.NoError(t, service.Close(ctx))
	})
	_, err = service.SetupConfig(t.Context(), request)
	require.NoError(t, err)

	replayed := receiveRIBCommit(t, commits)
	require.Len(t, replayed, 2)
	require.NotNil(t, replayed[0].GetRoute())
	require.Nil(t, replayed[1].GetRoute())
}

// Test_AdapterService_SetupConfig_ChainedReplacementsNeverOverlap verifies
// that an import replaced before it started still lets its successor send
// only after the import it replaced itself has exited.
//
// The first import is held in its shutdown by a blocking log write while a
// second setup replaces it and a third one replaces the second; the third
// import stays silent for an hour of fake time until the first one exits.
func Test_AdapterService_SetupConfig_ChainedReplacementsNeverOverlap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := newMemoryNetwork()
		serveBIRDExport(t, network.Listen("first.sock"), quietBIRDExport)
		serveBIRDExport(t, network.Listen("second.sock"), quietBIRDExport)
		thirdPrefix := netip.MustParsePrefix("2001:db8:3::/48")
		serveBIRDExport(t, network.Listen("third.sock"), func(idx int, connection net.Conn) error {
			_, _ = connection.Write(birdRouteChunk(3))
			_, _ = io.Copy(io.Discard, connection)
			return nil
		})
		firstCommitted := make(chan struct{}, 1)
		thirdSent := make(chan struct{}, 1)
		recorder := &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
			for {
				update, err := stream.Recv()
				if err != nil {
					return err
				}
				if update.GetHeartbeat() {
					continue
				}
				if update.GetRoute() == nil {
					select {
					case firstCommitted <- struct{}{}:
					default:
					}
					continue
				}
				prefix, err := update.GetRoute().GetPrefix().ToPrefix()
				if err == nil && prefix == thirdPrefix {
					select {
					case thirdSent <- struct{}{}:
					default:
					}
				}
			}
		}}
		serveRouteService(t, network.Listen(memoryRouteOperator), recorder)

		holdExit := &blockingLogCore{
			Message: "stopped BIRD import",
			Blocked: make(chan struct{}),
			Release: make(chan struct{}),
		}
		service := startMemoryImport(t, network, newImportRequest(t, "first.sock"),
			birdadapter.WithAdapterServiceLog(zap.New(holdExit)),
		)
		// Release the held import before the service closes.
		t.Cleanup(holdExit.Unblock)
		receiveEvent(t, firstCommitted)

		var setups errgroup.Group
		setups.Go(func() error {
			_, err := service.SetupConfig(t.Context(), newImportRequest(t, "second.sock"))
			return err
		})
		receiveEvent(t, holdExit.Blocked)
		requireImportSockets(t, service, "second.sock")

		setups.Go(func() error {
			_, err := service.SetupConfig(t.Context(), newImportRequest(t, "third.sock"))
			return err
		})
		requireImportSockets(t, service, "third.sock")

		time.Sleep(time.Hour)
		synctest.Wait()
		select {
		case <-thirdSent:
			t.Fatal("the third import sent while the first one was still running")
		default:
		}

		holdExit.Unblock()
		require.NoError(t, setups.Wait())
		receiveEvent(t, thirdSent)
	})
}

// requireImportSockets waits until every goroutine of the bubble blocks and
// requires the only import of the service to read socket then.
func requireImportSockets(t *testing.T, service *birdadapter.AdapterService, socket string) {
	t.Helper()
	synctest.Wait()
	sessions, err := service.ListSessions(t.Context(), &adapterpb.ListSessionsRequest{})
	require.NoError(t, err)
	require.Len(t, sessions.GetSessions(), 1)
	require.Equal(t, []string{socket}, sessions.GetSessions()[0].GetSockets())
}

// blockingLogCore discards every log entry except the first one with
// Message, whose write blocks until Unblock is called.
//
// Only the held write blocks, and on a channel, so the later writes leave
// a synctest bubble able to go idle.
type blockingLogCore struct {
	Message string
	// Blocked is closed once the held write has started.
	Blocked chan struct{}
	Release chan struct{}

	held    atomic.Bool
	release sync.Once
}

func (m *blockingLogCore) Enabled(zapcore.Level) bool { return true }

func (m *blockingLogCore) With([]zapcore.Field) zapcore.Core { return m }

func (m *blockingLogCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if entry.Message == m.Message {
		return checked.AddCore(entry, m)
	}
	return checked
}

func (m *blockingLogCore) Write(zapcore.Entry, []zapcore.Field) error {
	if m.held.CompareAndSwap(false, true) {
		close(m.Blocked)
		<-m.Release
	}
	return nil
}

func (m *blockingLogCore) Sync() error { return nil }

// Unblock lets the held write return; later calls do nothing.
func (m *blockingLogCore) Unblock() {
	m.release.Do(func() { close(m.Release) })
}

type feedRIBServerStream = grpc.ClientStreamingServer[routepb.Update, routepb.UpdateSummary]

// feedRIBRecorder numbers every FeedRIB stream it accepts, keeps its context
// and serves it with Handle.
type feedRIBRecorder struct {
	routepb.UnimplementedRouteServiceServer
	Handle func(idx int, stream feedRIBServerStream) error

	mu       sync.Mutex
	contexts []context.Context
}

func (m *feedRIBRecorder) FeedRIB(stream feedRIBServerStream) error {
	m.mu.Lock()
	idx := len(m.contexts)
	m.contexts = append(m.contexts, stream.Context())
	m.mu.Unlock()

	return m.Handle(idx, stream)
}

// Streams returns the contexts of the accepted streams in acceptance order;
// a context is done once its stream has ended.
func (m *feedRIBRecorder) Streams() []context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.contexts)
}

// gatedListener hands out connections until Refuse is set, and hangs up
// every later one at once.
type gatedListener struct {
	net.Listener
	Refuse atomic.Bool
}

func (m *gatedListener) Accept() (net.Conn, error) {
	for {
		connection, err := m.Listener.Accept()
		if err != nil || !m.Refuse.Load() {
			return connection, err
		}
		_ = connection.Close()
	}
}

// serveRouteService serves server on listener until the test ends and
// returns the gRPC server, so the test may stop it earlier.
func serveRouteService(
	t *testing.T,
	listener net.Listener,
	server routepb.RouteServiceServer,
	options ...grpc.ServerOption,
) *grpc.Server {
	t.Helper()
	grpcServer := grpc.NewServer(options...)
	routepb.RegisterRouteServiceServer(grpcServer, server)
	var workers errgroup.Group
	workers.Go(func() error { return grpcServer.Serve(listener) })
	t.Cleanup(func() {
		grpcServer.Stop()
		require.NoError(t, workers.Wait())
	})
	return grpcServer
}

// fixedFlowControlWindows returns server options that keep the flow-control
// windows at their minimum, so a stream nobody reads blocks its sender after
// a few hundred kilobytes.
func fixedFlowControlWindows() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.InitialWindowSize(64 * 1024),
		grpc.InitialConnWindowSize(64 * 1024),
	}
}

// newImportRequest returns the setup request of the import bird0 reading
// sockets and dumping every route as soon as it arrives.
func newImportRequest(t *testing.T, sockets ...string) *adapterpb.SetupConfigRequest {
	t.Helper()
	sourceV4, err := commonpb.NewIPv4AddressFromAddr(netip.MustParseAddr("192.0.2.1"))
	require.NoError(t, err)
	sourceV6, err := commonpb.NewIPv6AddressFromAddr(netip.MustParseAddr("2001:db8::1"))
	require.NoError(t, err)
	return &adapterpb.SetupConfigRequest{
		Name: "bird0",
		Config: &adapterpb.ImportConfig{
			Sockets:       sockets,
			DumpThreshold: 1,
		},
		SourceV4: sourceV4,
		SourceV6: sourceV6,
	}
}

// startImport sets up the import bird0 against endpoint and closes the
// service when the test ends, before the fixtures it reads from stop.
func startImport(t *testing.T, endpoint string, sockets ...string) *birdadapter.AdapterService {
	t.Helper()
	service := birdadapter.NewAdapterService(endpoint)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		require.NoError(t, service.Close(ctx))
	})
	_, err := service.SetupConfig(t.Context(), newImportRequest(t, sockets...))
	require.NoError(t, err)
	return service
}

// receiveOpened waits for the ordinal of the next stream a handler reports.
func receiveOpened(t *testing.T, opened <-chan int) int {
	t.Helper()
	select {
	case idx := <-opened:
		return idx
	case <-time.After(5 * time.Second):
		t.Fatal("no FeedRIB stream reported its updates")
		return -1
	}
}

// startBIRDExport serves a BIRD export socket, passing every accepted
// connection and its ordinal to serve, and closes the connections when the
// test ends.
func startBIRDExport(t *testing.T, serve func(idx int, connection net.Conn) error) string {
	t.Helper()
	// Use a short path because Unix socket names have a platform length limit.
	directory, err := os.MkdirTemp("", "bird-export-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(directory)) })
	socket := filepath.Join(directory, "export.sock")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	serveBIRDExport(t, listener, serve)
	return socket
}

// serveBIRDExport passes every connection listener accepts and its ordinal
// to serve, and closes listener and the connections when the test ends.
func serveBIRDExport(t *testing.T, listener net.Listener, serve func(idx int, connection net.Conn) error) {
	t.Helper()
	var workers errgroup.Group
	workers.Go(func() error {
		for idx := 0; ; idx++ {
			connection, err := listener.Accept()
			if err != nil {
				return nil
			}
			workers.Go(func() error {
				defer connection.Close()
				stop := context.AfterFunc(t.Context(), func() { _ = connection.Close() })
				defer stop()
				return serve(idx, connection)
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		require.NoError(t, workers.Wait())
	})
}

// startQuietBIRDExport serves a BIRD export socket that serves every
// connection with quietBIRDExport.
func startQuietBIRDExport(t *testing.T) string {
	t.Helper()
	return startBIRDExport(t, quietBIRDExport)
}

// quietBIRDExport announces one route to connection and then stays quiet,
// whether or not the import hangs up first.
func quietBIRDExport(idx int, connection net.Conn) error {
	_, _ = connection.Write(birdRouteChunk(1))
	_, _ = io.Copy(io.Discard, connection)
	return nil
}

// floodBIRDExport writes routes until the reader stops taking them for a
// while, which happens once the import is stuck sending them on.
func floodBIRDExport(connection net.Conn) {
	chunk := birdRouteChunk(1)
	for {
		if err := connection.SetWriteDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			return
		}
		if _, err := connection.Write(chunk); err != nil {
			return
		}
	}
}

// birdRouteChunk returns a BIRD export chunk announcing 2001:db8:<group>::/48
// via 2001:db8::1.
func birdRouteChunk(group byte) []byte {
	update := []byte{
		0: 92,
		4: 2, 5: 48, 6: 20,
		8: 0xb8, 9: 0x0d, 10: 1, 11: 0x20,
		14: group,
		44: 1,
		68: 24,
		72: 3, 73: 4, 76: 16,
		80: 0xb8, 81: 0x0d, 82: 1, 83: 0x20,
		92: 1, 95: 0,
	}
	binary.LittleEndian.PutUint32(update[:4], uint32(len(update)-4))
	return update
}

// receivedUpdate is an update a route operator received and when.
type receivedUpdate struct {
	At     time.Time
	Update *routepb.Update
}

// memoryRouteOperator is the address the route operator listens on in a
// memory network.
const memoryRouteOperator = "route-operator"

// startMemoryImport sets up the import request describes against the route
// operator and the BIRD sockets bound on network, and closes the service
// when the test ends, before the fixtures it reads from stop.
func startMemoryImport(
	t *testing.T,
	network *memoryNetwork,
	request *adapterpb.SetupConfigRequest,
	options ...birdadapter.AdapterServiceOption,
) *birdadapter.AdapterService {
	t.Helper()
	options = append(options,
		birdadapter.WithRouteOperatorDialOptions(
			grpc.WithContextDialer(network.Dial),
			// The zstd codec pools its decoders process-wide, and a decoder
			// made in one bubble must never run in another.
			grpc.WithDefaultCallOptions(grpc.UseCompressor(encoding.Identity)),
		),
		birdadapter.WithBIRDDialer(network.Dial),
	)
	service := birdadapter.NewAdapterService("passthrough:///"+memoryRouteOperator, options...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		require.NoError(t, service.Close(ctx))
	})
	_, err := service.SetupConfig(t.Context(), request)
	require.NoError(t, err)
	return service
}

// rejectingRecorder returns a route operator that fails every FeedRIB stream
// at once, reporting the time each one opened on opened while it has room.
func rejectingRecorder(opened chan<- time.Time) *feedRIBRecorder {
	return &feedRIBRecorder{Handle: func(idx int, stream feedRIBServerStream) error {
		select {
		case opened <- time.Now():
		default:
		}
		return status.Error(codes.Unavailable, "route operator rejects the stream")
	}}
}

// receiveEvent waits for the next value on events for at most an hour,
// of fake time inside a bubble.
func receiveEvent[T any](t *testing.T, events <-chan T) T {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Hour):
		t.Fatal("the awaited event did not happen within an hour")
		var zero T
		return zero
	}
}

// memoryNetwork connects dialers to the bufconn listeners bound on it by
// address, whose connections block on condition variables: a goroutine
// waiting on one counts as durably blocked inside a synctest bubble, which a
// goroutine waiting on a socket never does.
type memoryNetwork struct {
	mu        sync.Mutex
	listeners map[string]*bufconn.Listener
}

func newMemoryNetwork() *memoryNetwork {
	return &memoryNetwork{listeners: map[string]*bufconn.Listener{}}
}

// Listen binds a new listener to address.
func (m *memoryNetwork) Listen(address string) *bufconn.Listener {
	m.mu.Lock()
	defer m.mu.Unlock()

	listener := bufconn.Listen(64 * 1024)
	m.listeners[address] = listener
	return listener
}

// Dial connects to the listener bound to address, and fails as a refused
// connection does when none is.
func (m *memoryNetwork) Dial(ctx context.Context, address string) (net.Conn, error) {
	m.mu.Lock()
	listener := m.listeners[address]
	m.mu.Unlock()

	if listener == nil {
		return nil, &net.OpError{Op: "dial", Net: "memory", Err: syscall.ECONNREFUSED}
	}
	return listener.DialContext(ctx)
}
