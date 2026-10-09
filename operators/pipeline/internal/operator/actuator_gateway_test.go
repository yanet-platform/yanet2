package operator_test

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
	"github.com/yanet-platform/yanet2/operators/pipeline/internal/operator"
)

// vxlanGateway is a gateway that serves only the vxlan device service and
// records every update it receives.
type vxlanGateway struct {
	vxlanpb.UnimplementedDeviceVxlanServiceServer

	mu       sync.Mutex
	requests []*vxlanpb.UpdateDeviceVxlanRequest
	err      error
}

func (m *vxlanGateway) UpdateDevice(
	ctx context.Context,
	request *vxlanpb.UpdateDeviceVxlanRequest,
) (*vxlanpb.UpdateDeviceVxlanResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests = append(m.requests, request)
	if m.err != nil {
		return nil, m.err
	}

	return &vxlanpb.UpdateDeviceVxlanResponse{}, nil
}

// received returns the updates the gateway has served so far.
func (m *vxlanGateway) received() []*vxlanpb.UpdateDeviceVxlanRequest {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]*vxlanpb.UpdateDeviceVxlanRequest(nil), m.requests...)
}

// resourceUpdates is a metrics observer that records every resource update
// outcome by kind.
type resourceUpdates struct {
	mu      sync.Mutex
	updates []resourceUpdate
}

type resourceUpdate struct {
	kind string
	err  error
}

func (m *resourceUpdates) OnApplyCompleted(error) {}

func (m *resourceUpdates) OnResourceUpdated(kind string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updates = append(m.updates, resourceUpdate{kind: kind, err: err})
}

func (m *resourceUpdates) OnGC(int, int, error) {}

// recorded returns the resource updates observed so far.
func (m *resourceUpdates) recorded() []resourceUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]resourceUpdate(nil), m.updates...)
}

// newVxlanActuator serves the gateway on a loopback port and returns an
// actuator dialed to it.
func newVxlanActuator(
	t *testing.T,
	gateway *vxlanGateway,
	observer *resourceUpdates,
) *operator.GatewayActuator {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	vxlanpb.RegisterDeviceVxlanServiceServer(server, gateway)

	var group errgroup.Group
	group.Go(func() error { return server.Serve(listener) })
	t.Cleanup(func() {
		server.Stop()
		_ = group.Wait()
	})

	actuator, err := operator.NewGatewayActuator(
		commonoperator.GatewayConfig{
			Name:     "gw0",
			Endpoint: xcfg.MustNonEmptyString(listener.Addr().String()),
		},
		operator.WithGatewayActuatorMetrics(observer),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = actuator.Close() })

	return actuator
}

// vxlanStage returns a stage holding one vxlan device with the given tunnel
// and one input and one output binding.
func vxlanStage(tunnel operator.VXLANTunnelConfig) *operator.StageConfig {
	return &operator.StageConfig{
		Name: "stage",
		Devices: operator.DevicesConfig{
			VXLAN: []operator.VXLANDeviceConfig{{
				Name:   "vx0",
				Tunnel: tunnel,
				Input:  []operator.PipelineRefConfig{{Name: "p-in", Weight: 3}},
				Output: []operator.PipelineRefConfig{{Name: "p-out", Weight: 1}},
			}},
		},
	}
}

// validVxlanTunnel returns a tunnel whose addresses are all well formed.
func validVxlanTunnel() operator.VXLANTunnelConfig {
	return operator.VXLANTunnelConfig{
		LocalIP:   "192.0.2.1",
		RemoteIP:  "198.51.100.7",
		LocalMAC:  "02:00:00:00:00:01",
		RemoteMAC: "02:00:00:00:00:02",
		VNI:       4660,
	}
}

// Test_GatewayActuator_Apply_SendsVxlanDevice verifies that Apply turns the
// text addresses of a configured vxlan device into the typed tunnel of one
// update request and reports the update as a success.
func Test_GatewayActuator_Apply_SendsVxlanDevice(t *testing.T) {
	gateway := &vxlanGateway{}
	observer := &resourceUpdates{}
	actuator := newVxlanActuator(t, gateway, observer)

	require.NoError(t, actuator.Apply(t.Context(), vxlanStage(validVxlanTunnel())))

	requests := gateway.received()
	require.Len(t, requests, 1)
	require.True(t, proto.Equal(&vxlanpb.UpdateDeviceVxlanRequest{
		Name: "vx0",
		Device: &commonpb.Device{
			Input:  []*commonpb.DevicePipeline{{Name: "p-in", Weight: 3}},
			Output: []*commonpb.DevicePipeline{{Name: "p-out", Weight: 1}},
		},
		Tunnel: &vxlanpb.VxlanTunnel{
			LocalIp:   commonpb.NewIPv4Address([4]byte{192, 0, 2, 1}),
			RemoteIp:  commonpb.NewIPv4Address([4]byte{198, 51, 100, 7}),
			LocalMac:  commonpb.NewMACAddressEUI48([6]byte{0x02, 0, 0, 0, 0, 0x01}),
			RemoteMac: commonpb.NewMACAddressEUI48([6]byte{0x02, 0, 0, 0, 0, 0x02}),
			Vni:       4660,
		},
	}, requests[0]), "request: %v", requests[0])
	require.Equal(t, []resourceUpdate{{kind: "device-vxlan"}}, observer.recorded())
}

// Test_GatewayActuator_Apply_RejectsMalformedVxlanTunnel verifies that a
// tunnel address that is not in its text form fails the apply, names the
// field, counts as a failed vxlan update and never reaches the gateway.
func Test_GatewayActuator_Apply_RejectsMalformedVxlanTunnel(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(tunnel *operator.VXLANTunnelConfig)
		message string
	}{
		{
			name:    "missing local IP",
			mutate:  func(tunnel *operator.VXLANTunnelConfig) { tunnel.LocalIP = "" },
			message: "local_ip",
		},
		{
			name:    "malformed remote IP",
			mutate:  func(tunnel *operator.VXLANTunnelConfig) { tunnel.RemoteIP = "198.51.100" },
			message: "remote_ip",
		},
		{
			name:    "IPv6 local IP",
			mutate:  func(tunnel *operator.VXLANTunnelConfig) { tunnel.LocalIP = "2001:db8::1" },
			message: "local_ip: not an IPv4 address",
		},
		{
			name:    "malformed local MAC",
			mutate:  func(tunnel *operator.VXLANTunnelConfig) { tunnel.LocalMAC = "02:00:00:00:00" },
			message: "local_mac",
		},
		{
			name: "EUI-64 remote MAC",
			mutate: func(tunnel *operator.VXLANTunnelConfig) {
				tunnel.RemoteMAC = "02:00:00:00:00:00:00:02"
			},
			message: "remote_mac",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gateway := &vxlanGateway{}
			observer := &resourceUpdates{}
			actuator := newVxlanActuator(t, gateway, observer)

			tunnel := validVxlanTunnel()
			tc.mutate(&tunnel)
			err := actuator.Apply(t.Context(), vxlanStage(tunnel))

			require.ErrorContains(t, err, `update vxlan device "vx0"`)
			require.ErrorContains(t, err, tc.message)
			require.Empty(t, gateway.received())
			updates := observer.recorded()
			require.Len(t, updates, 1)
			require.Equal(t, "device-vxlan", updates[0].kind)
			require.Error(t, updates[0].err)
		})
	}
}

// Test_GatewayActuator_Apply_VxlanGatewayRefusal verifies that a refusal of
// the gateway fails the apply with the gateway's status and counts as a
// failed vxlan update.
func Test_GatewayActuator_Apply_VxlanGatewayRefusal(t *testing.T) {
	gateway := &vxlanGateway{err: status.Error(codes.InvalidArgument, "tunnel: vni is out of range")}
	observer := &resourceUpdates{}
	actuator := newVxlanActuator(t, gateway, observer)

	err := actuator.Apply(t.Context(), vxlanStage(validVxlanTunnel()))

	require.ErrorContains(t, err, `update vxlan device "vx0"`)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	updates := observer.recorded()
	require.Len(t, updates, 1)
	require.Equal(t, "device-vxlan", updates[0].kind)
	require.Error(t, updates[0].err)
}
