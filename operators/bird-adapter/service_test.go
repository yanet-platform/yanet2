package bird_adapter_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
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
