package operator_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// actuatorBackend returns a complete kernel dump or a discovery failure.
type actuatorBackend struct {
	fakeNetlinkHandle
	Operations *[]string
	Failure    error
	Cancel     context.CancelFunc
}

func (m *actuatorBackend) WalkNeighbours(ctx context.Context, visit func(vnetlink.Neigh) error) error {
	*m.Operations = append(*m.Operations, "discovery")
	if m.Cancel != nil {
		m.Cancel()
	}
	return m.Failure
}

// actuatorClient records the actual replacement transport boundary.
type actuatorClient struct {
	Operations *[]string
	Failure    error
	Requests   []*operatorpb.ReplaceNeighboursRequest
}

func (m *actuatorClient) ReplaceNeighbours(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest, options ...grpc.CallOption) (*operatorpb.ReplaceNeighboursResponse, error) {
	*m.Operations = append(*m.Operations, "publication")
	if m.Failure != nil {
		return nil, m.Failure
	}
	m.Requests = append(m.Requests, request)
	return &operatorpb.ReplaceNeighboursResponse{}, nil
}

// actuatorFixture runs real discovery and publication over controlled I/O.
type actuatorFixture struct {
	Operations []string
	Backend    *actuatorBackend
	Client     *actuatorClient
	Actuator   *sidecaroperator.Actuator
}

// newActuatorFixture supplies a complete, empty namespace snapshot.
func newActuatorFixture() *actuatorFixture {
	fixture := &actuatorFixture{}
	fixture.Backend = &actuatorBackend{Operations: &fixture.Operations}
	fixture.Client = &actuatorClient{Operations: &fixture.Operations}
	fixture.Actuator = sidecaroperator.NewActuator(
		fixture.Backend,
		[]neighbour.GatewayTarget{{Name: "gateway", Client: fixture.Client}}, nil,
		neighbour.PublicationConfig{TableName: "netlink-dataplane-default", DefaultPriority: 100, Timeout: time.Second},
		nil,
	)
	return fixture
}

// Test_Actuator_DiscoveryFailure verifies that incomplete dumps cannot clear
// previously published state by sending an empty replacement.
func Test_Actuator_DiscoveryFailure(t *testing.T) {
	fixture := newActuatorFixture()
	fixture.Backend.Failure = vnetlink.ErrDumpInterrupted
	require.ErrorIs(t, fixture.Actuator.Apply(t.Context(), sidecaroperator.State{}), vnetlink.ErrDumpInterrupted)
	require.Equal(t, []string{"discovery"}, fixture.Operations)
}

// Test_Actuator_PublicationFailure verifies that a transport error propagates
// to the common reconciliation retry loop.
func Test_Actuator_PublicationFailure(t *testing.T) {
	fixture := newActuatorFixture()
	fixture.Client.Failure = net.ErrClosed
	require.ErrorIs(t, fixture.Actuator.Apply(t.Context(), sidecaroperator.State{}), net.ErrClosed)
}

// Test_Actuator_CancellationDuringDiscovery verifies that cancellation between
// stages prevents further kernel and transport I/O.
func Test_Actuator_CancellationDuringDiscovery(t *testing.T) {
	fixture := newActuatorFixture()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.Backend.Cancel = cancel
	require.ErrorIs(t, fixture.Actuator.Apply(ctx, sidecaroperator.State{}), context.Canceled)
	require.Equal(t, []string{"discovery"}, fixture.Operations)
}

// Test_Actuator_EmptySnapshot verifies that a complete empty dump sends the
// owned table's explicit empty replacement rather than skipping publication.
func Test_Actuator_EmptySnapshot(t *testing.T) {
	fixture := newActuatorFixture()
	require.NoError(t, fixture.Actuator.Apply(t.Context(), sidecaroperator.State{}))
	require.Len(t, fixture.Client.Requests, 1)
	require.Equal(t, "netlink-dataplane-default", fixture.Client.Requests[0].GetTable())
	require.Empty(t, fixture.Client.Requests[0].GetEntries())
	require.Equal(t, []string{"discovery", "publication"}, fixture.Operations)
}
