package operator_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// recordingGateway is a fake Gateway reporting the endpoints it is sent.
type recordingGateway struct {
	ynpb.UnimplementedGatewayServer

	endpoints chan string
}

func newRecordingGateway() *recordingGateway {
	return &recordingGateway{
		endpoints: make(chan string, 16),
	}
}

func (m *recordingGateway) Register(
	_ context.Context,
	request *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	select {
	case m.endpoints <- request.GetBackend().GetEndpoint():
	default:
	}

	return &ynpb.RegisterResponse{}, nil
}

// serveRecordingGateway starts a fake Gateway on a loopback port, stopping it
// when the test ends, and returns it with the endpoint to dial.
func serveRecordingGateway(t *testing.T) (*recordingGateway, string) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	service := newRecordingGateway()
	server := grpc.NewServer()
	ynpb.RegisterGatewayServer(server, service)

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	return service, listener.Addr().String()
}

// awaitEndpoint returns the endpoint of the next registration, failing on
// timeout.
func awaitEndpoint(t *testing.T, service *recordingGateway) string {
	t.Helper()

	select {
	case endpoint := <-service.endpoints:
		return endpoint
	case <-time.After(10 * time.Second):
		t.Fatal("gateway accepted no registration")
		return ""
	}
}

// verifies that the runner registers the given endpoint as written, keeping a
// name unresolved for the gateway to resolve on each dial.
func Test_GatewayRegRunner_Run_RegistersGivenEndpointVerbatim(t *testing.T) {
	t.Parallel()

	const advertised = "route-operator.yanet.svc.cluster.local:8080"

	service, gatewayEndpoint := serveRecordingGateway(t)

	runner := operator.NewGatewayRegRunner(
		[]operator.GatewayConfig{{
			Name:     "numa0",
			Endpoint: xcfg.MustNonEmptyString(gatewayEndpoint),
		}},
		[]string{"fake.Service"},
		advertised,
		operator.WithGatewayRegInterval(time.Second),
		operator.WithGatewayRegLog(zap.NewNop()),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		_ = runner.Run(ctx)
	}()

	require.Equal(t, advertised, awaitEndpoint(t, service))
}
