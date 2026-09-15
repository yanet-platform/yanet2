package main

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Test_ShippedDefaultConfig_MatchesServerConfig guards against drift between
// the shipped default config file and the ServerConfig struct.
//
// With strict parsing enabled a stale or renamed key in the shipped YAML
// would break every fresh install, so this test loads the conffile through
// xcfg.WithKnownFields() and requires it to decode to the server defaults plus
// its explicitly present BIRD block. That also catches the Go defaults
// silently drifting away from the shipped conffile.
func Test_ShippedDefaultConfig_MatchesServerConfig(t *testing.T) {
	cfg, err := xcfg.LoadConfig[ServerConfig]("../../etc/yanet/bird-adapter-default.yaml", xcfg.WithKnownFields())
	require.NoError(t, err)

	expected := DefaultServerConfig()
	expectedBIRD := BIRDConfig{}
	expectedBIRD.Default()
	expected.BIRD = xcfg.NewOptional(expectedBIRD)
	require.Equal(t, expected, cfg)
}

// Test_LoadConfig_BIRDBlockSemantics covers absent and present startup BIRD
// configuration, including nested defaults and explicit disabling.
func Test_LoadConfig_BIRDBlockSemantics(t *testing.T) {
	defaultBIRD := BIRDConfig{}
	defaultBIRD.Default()
	partialBIRD := defaultBIRD
	partialBIRD.Name = "route1"
	emptyNameBIRD := defaultBIRD
	emptyNameBIRD.Name = ""

	tests := []struct {
		name     string
		config   []byte
		wantBIRD *BIRDConfig
	}{
		{
			name: "old-style config without bird block",
			config: []byte(`logging:
  level: info
listen_addr: "localhost:50051"
route_operator_endpoint: "[::1]:8080"
`),
		},
		{
			name:     "present block gets nested defaults",
			config:   []byte("bird:\n  name: route1\n"),
			wantBIRD: &partialBIRD,
		},
		{
			name:     "empty name disables import",
			config:   []byte("bird:\n  name: \"\"\n"),
			wantBIRD: &emptyNameBIRD,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "server.yaml")
			require.NoError(t, os.WriteFile(configPath, tc.config, 0o600))

			cfg, err := xcfg.LoadConfig[ServerConfig](configPath, xcfg.WithKnownFields())
			require.NoError(t, err)
			if tc.wantBIRD == nil {
				require.Nil(t, cfg.BIRD.Unwrap())
				return
			}

			require.Equal(t, tc.wantBIRD, cfg.BIRD.Unwrap())
		})
	}
}

// Test_BIRDConfig_Validate asserts that an empty name disables the startup
// import regardless of the other fields, and that a filled-in name rejects
// a half-filled or wrong-family config.
func Test_BIRDConfig_Validate(t *testing.T) {
	validSockets := []string{"/var/run/bird/yanet-master4.sock"}
	validV4 := netip.MustParseAddr("127.0.0.1")
	validV6 := netip.MustParseAddr("::1")

	tests := []struct {
		name    string
		cfg     BIRDConfig
		wantErr bool
	}{
		{
			name:    "disabled",
			cfg:     BIRDConfig{},
			wantErr: false,
		},
		{
			name: "empty name disables import despite sockets and sources set",
			cfg: BIRDConfig{
				Sockets:  validSockets,
				SourceV4: validV4,
				SourceV6: validV6,
			},
			wantErr: false,
		},
		{
			name: "missing sockets",
			cfg: BIRDConfig{
				Name:     "route0",
				SourceV4: validV4,
				SourceV6: validV6,
			},
			wantErr: true,
		},
		{
			name: "missing source addresses",
			cfg: BIRDConfig{
				Name:    "route0",
				Sockets: validSockets,
			},
			wantErr: true,
		},
		{
			name: "ipv6 address in source_v4",
			cfg: BIRDConfig{
				Name:     "route0",
				Sockets:  validSockets,
				SourceV4: validV6,
				SourceV6: validV6,
			},
			wantErr: true,
		},
		{
			name: "ipv4-mapped address in source_v6",
			cfg: BIRDConfig{
				Name:     "route0",
				Sockets:  validSockets,
				SourceV4: validV4,
				SourceV6: netip.MustParseAddr("::ffff:127.0.0.1"),
			},
			wantErr: true,
		},
		{
			name: "fully valid",
			cfg: BIRDConfig{
				Name:     "route0",
				Sockets:  validSockets,
				SourceV4: validV4,
				SourceV6: validV6,
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// validateProbeRequest decodes through an embedded well-known proto type, so
// this package's probe service needs no generated message of its own.
type validateProbeRequest struct {
	*wrapperspb.StringValue
}

func (m *validateProbeRequest) Validate() error {
	if m.GetValue() == "" {
		return errors.New("value is required")
	}
	return nil
}

type validateProbeServer interface {
	Unary(ctx context.Context, req *validateProbeRequest) (*emptypb.Empty, error)
	Stream(req *validateProbeRequest, stream grpc.ServerStreamingServer[emptypb.Empty]) error
}

// recordingValidateProbeServer records whether its handlers ran, so a
// wiring test can assert an invalid probe request never reaches them.
type recordingValidateProbeServer struct {
	unaryCalled  atomic.Bool
	streamCalled atomic.Bool
}

func (m *recordingValidateProbeServer) Unary(ctx context.Context, req *validateProbeRequest) (*emptypb.Empty, error) {
	m.unaryCalled.Store(true)
	return &emptypb.Empty{}, nil
}

func (m *recordingValidateProbeServer) Stream(req *validateProbeRequest, stream grpc.ServerStreamingServer[emptypb.Empty]) error {
	m.streamCalled.Store(true)
	return stream.Send(&emptypb.Empty{})
}

const validateProbeServiceName = "yanet.test.ValidateProbe"

// validateProbeServiceDesc is a hand-written grpc.ServiceDesc mirroring the
// shape protoc-gen-go-grpc emits.
//
// It lets this package drive a real unary and a real server-streaming RPC
// without a compiled proto package of its own.
var validateProbeServiceDesc = grpc.ServiceDesc{
	ServiceName: validateProbeServiceName,
	HandlerType: (*validateProbeServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Unary",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				req := &validateProbeRequest{StringValue: &wrapperspb.StringValue{}}
				if err := dec(req); err != nil {
					return nil, err
				}
				if interceptor == nil {
					return srv.(validateProbeServer).Unary(ctx, req)
				}
				info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/" + validateProbeServiceName + "/Unary"}
				handler := func(ctx context.Context, req any) (any, error) {
					return srv.(validateProbeServer).Unary(ctx, req.(*validateProbeRequest))
				}
				return interceptor(ctx, req, info, handler)
			},
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Stream",
			ServerStreams: true,
			Handler: func(srv any, stream grpc.ServerStream) error {
				req := &validateProbeRequest{StringValue: &wrapperspb.StringValue{}}
				if err := stream.RecvMsg(req); err != nil {
					return err
				}
				return srv.(validateProbeServer).Stream(req, &grpc.GenericServerStream[validateProbeRequest, emptypb.Empty]{ServerStream: stream})
			},
		},
	},
	Metadata: "validate_probe_test",
}

// startValidateProbeServer serves the probe on the adapter's real gRPC server
// until the test ends.
func startValidateProbeServer(t *testing.T) (probe *recordingValidateProbeServer, conn *grpc.ClientConn) {
	t.Helper()

	probe = &recordingValidateProbeServer{}
	server := newGRPCServer()
	server.RegisterService(&validateProbeServiceDesc, probe)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var group errgroup.Group
	group.Go(func() error { return server.Serve(listener) })
	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, group.Wait())
	})

	conn, err = grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return probe, conn
}

// Test_NewGRPCServer_RejectsInvalidProbeUnary verifies that an invalid unary
// request never reaches the handler.
func Test_NewGRPCServer_RejectsInvalidProbeUnary(t *testing.T) {
	t.Parallel()

	probe, conn := startValidateProbeServer(t)

	invokeErr := conn.Invoke(t.Context(), "/"+validateProbeServiceName+"/Unary", &wrapperspb.StringValue{}, &emptypb.Empty{})

	statusErr, ok := status.FromError(invokeErr)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, statusErr.Code())
	require.Equal(t, "value is required", statusErr.Message())
	require.False(t, probe.unaryCalled.Load(), "handler must not run for an invalid probe request")
}

// Test_NewGRPCServer_RejectsInvalidProbeStream verifies that an invalid
// streamed request never reaches the handler.
func Test_NewGRPCServer_RejectsInvalidProbeStream(t *testing.T) {
	t.Parallel()

	probe, conn := startValidateProbeServer(t)

	stream, err := conn.NewStream(t.Context(), &grpc.StreamDesc{StreamName: "Stream", ServerStreams: true}, "/"+validateProbeServiceName+"/Stream")
	require.NoError(t, err)
	require.NoError(t, stream.SendMsg(&wrapperspb.StringValue{}))
	require.NoError(t, stream.CloseSend())

	recvErr := stream.RecvMsg(&emptypb.Empty{})
	statusErr, ok := status.FromError(recvErr)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, statusErr.Code())
	require.Equal(t, "value is required", statusErr.Message())
	require.False(t, probe.streamCalled.Load(), "handler must not run for an invalid probe request")
}
