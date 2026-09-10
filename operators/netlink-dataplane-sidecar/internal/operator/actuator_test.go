package operator_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// actuatorLinks controls restoration at its kernel boundary.
type actuatorLinks struct {
	Operations *[]string
	Failure    error
	Cancel     context.CancelFunc
}

func (m *actuatorLinks) Apply(ctx context.Context, state netplan.State) error {
	*m.Operations = append(*m.Operations, "links")
	if m.Cancel != nil {
		m.Cancel()
	}
	return m.Failure
}

// actuatorBackend returns a complete kernel dump or a discovery failure.
type actuatorBackend struct {
	fakeNetlinkHandle
	Operations *[]string
	Failure    error
}

func (m *actuatorBackend) WalkNeighbours(ctx context.Context, visit func(vnetlink.Neigh) error) error {
	*m.Operations = append(*m.Operations, "discovery")
	return m.Failure
}

// actuatorClient records the actual replacement transport boundary.
type actuatorClient struct {
	Operations *[]string
	Failure    error
	Chunks     []*operatorpb.ReplaceNeighboursRequest
}

func (m *actuatorClient) ReplaceNeighbours(ctx context.Context, options ...grpc.CallOption) (grpc.ClientStreamingClient[operatorpb.ReplaceNeighboursRequest, operatorpb.ReplaceNeighboursResponse], error) {
	*m.Operations = append(*m.Operations, "publication")
	if m.Failure != nil {
		return nil, m.Failure
	}
	return &actuatorStream{Client: m}, nil
}

// actuatorStream acknowledges only after all requested chunks are sent.
type actuatorStream struct {
	grpc.ClientStream
	Client *actuatorClient
}

func (m *actuatorStream) Send(chunk *operatorpb.ReplaceNeighboursRequest) error {
	m.Client.Chunks = append(m.Client.Chunks, chunk)
	return nil
}

func (m *actuatorStream) CloseAndRecv() (*operatorpb.ReplaceNeighboursResponse, error) {
	return &operatorpb.ReplaceNeighboursResponse{}, nil
}

// actuatorFixture runs real discovery and publication over controlled I/O.
type actuatorFixture struct {
	Operations []string
	Links      *actuatorLinks
	Backend    *actuatorBackend
	Client     *actuatorClient
	Actuator   *sidecaroperator.Actuator
}

// newActuatorFixture supplies a complete, empty namespace snapshot.
func newActuatorFixture() *actuatorFixture {
	fixture := &actuatorFixture{}
	fixture.Links = &actuatorLinks{Operations: &fixture.Operations}
	fixture.Backend = &actuatorBackend{Operations: &fixture.Operations}
	fixture.Client = &actuatorClient{Operations: &fixture.Operations}
	fixture.Actuator = sidecaroperator.NewActuator(
		fixture.Links, fixture.Backend,
		[]neighbour.GatewayTarget{{Name: "gateway", Client: fixture.Client}}, nil,
		neighbour.PublicationConfig{TableName: "netlink-dataplane-default", DefaultPriority: 100, Timeout: time.Second},
		nil,
	)
	return fixture
}

// Test_Actuator_StageOrder verifies that real discovery runs after restoration
// and completes before any replacement transport opens.
func Test_Actuator_StageOrder(t *testing.T) {
	fixture := newActuatorFixture()
	require.NoError(t, fixture.Actuator.Apply(t.Context(), sidecaroperator.State{}))
	require.Equal(t, []string{"links", "discovery", "publication"}, fixture.Operations)
}

// Test_Actuator_RestorationFailure verifies that failed restoration prevents
// both the neighbour dump and publication.
func Test_Actuator_RestorationFailure(t *testing.T) {
	fixture := newActuatorFixture()
	fixture.Links.Failure = errors.New("restore failed")
	require.ErrorIs(t, fixture.Actuator.Apply(t.Context(), sidecaroperator.State{}), fixture.Links.Failure)
	require.Equal(t, []string{"links"}, fixture.Operations)
}

// Test_Actuator_DiscoveryFailure verifies that incomplete dumps cannot clear
// previously published state by opening an empty replacement stream.
func Test_Actuator_DiscoveryFailure(t *testing.T) {
	fixture := newActuatorFixture()
	fixture.Backend.Failure = vnetlink.ErrDumpInterrupted
	require.ErrorIs(t, fixture.Actuator.Apply(t.Context(), sidecaroperator.State{}), vnetlink.ErrDumpInterrupted)
	require.Equal(t, []string{"links", "discovery"}, fixture.Operations)
}

// Test_Actuator_PublicationFailure verifies that a transport error propagates
// to the common reconciliation retry loop.
func Test_Actuator_PublicationFailure(t *testing.T) {
	fixture := newActuatorFixture()
	fixture.Client.Failure = net.ErrClosed
	require.ErrorIs(t, fixture.Actuator.Apply(t.Context(), sidecaroperator.State{}), net.ErrClosed)
}

// Test_Actuator_CancellationAfterRestore verifies that cancellation between
// stages prevents further kernel and transport I/O.
func Test_Actuator_CancellationAfterRestore(t *testing.T) {
	fixture := newActuatorFixture()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.Links.Cancel = cancel
	require.ErrorIs(t, fixture.Actuator.Apply(ctx, sidecaroperator.State{}), context.Canceled)
	require.Equal(t, []string{"links"}, fixture.Operations)
}

// Test_Actuator_EmptySnapshot verifies that a complete empty dump sends the
// owned table's explicit empty replacement rather than skipping publication.
func Test_Actuator_EmptySnapshot(t *testing.T) {
	fixture := newActuatorFixture()
	require.NoError(t, fixture.Actuator.Apply(t.Context(), sidecaroperator.State{}))
	require.Len(t, fixture.Client.Chunks, 1)
	require.Equal(t, "netlink-dataplane-default", fixture.Client.Chunks[0].GetTable())
	require.Empty(t, fixture.Client.Chunks[0].GetEntries())
}
