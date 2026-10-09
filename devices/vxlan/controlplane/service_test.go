package vxlan_test

import (
	"context"
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	vxlan "github.com/yanet-platform/yanet2/devices/vxlan/controlplane"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// validTunnel returns a tunnel both the Go and the Rust rules accept.
func validTunnel() *vxlanpb.VxlanTunnel {
	return &vxlanpb.VxlanTunnel{
		LocalIp:   commonpb.NewIPv4Address([4]byte{192, 0, 2, 1}),
		RemoteIp:  commonpb.NewIPv4Address([4]byte{198, 51, 100, 7}),
		LocalMac:  commonpb.NewMACAddressEUI48([6]byte{0x02, 0, 0, 0, 0, 0x01}),
		RemoteMac: commonpb.NewMACAddressEUI48([6]byte{0x02, 0, 0, 0, 0, 0x02}),
		Vni:       0x1234,
	}
}

// newVxlanService returns a vxlan service, its agent and the shared memory of
// a fresh single-worker harness that loads the given device types.
//
// The harness and the agent are released when the test ends.
func newVxlanService(
	t *testing.T,
	devicesToLoad ...string,
) (*vxlan.DeviceVxlanService, *ffi.Agent, *ffi.SharedMemory) {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		DevicesToLoad: devicesToLoad,
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()
	agent, err := shm.AgentAttach("vxlan", 0, datasize.MB*2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return vxlan.NewDeviceVxlanService(agent), agent, shm
}

// newVxlanClient serves the service over an in-memory gRPC connection that
// runs the validate interceptor the gateway chains in front of every device
// service.
func newVxlanClient(t *testing.T, service *vxlan.DeviceVxlanService) vxlanpb.DeviceVxlanServiceClient {
	t.Helper()

	listener := bufconn.Listen(1 << 16)
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(xgrpc.ValidateUnaryInterceptor()))
	vxlanpb.RegisterDeviceVxlanServiceServer(server, service)

	var group errgroup.Group
	group.Go(func() error { return server.Serve(listener) })
	t.Cleanup(func() {
		server.Stop()
		_ = group.Wait()
	})

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return vxlanpb.NewDeviceVxlanServiceClient(conn)
}

// freeBytesForAgent returns the free byte count reported for the named
// agent's first instance.
func freeBytesForAgent(t *testing.T, shm *ffi.SharedMemory, name string) uint64 {
	t.Helper()

	for _, agentInfo := range shm.DPConfig(0).Agents() {
		if agentInfo.Name == name {
			require.NotEmpty(t, agentInfo.Instances)
			return agentInfo.Instances[0].FreeBytes
		}
	}

	t.Fatalf("agent %q not found in dataplane config", name)
	return 0
}

// Test_CheckABI_MatchesLinkedArchive verifies that the Rust control-plane
// archive linked into the test binary speaks the ABI version the header
// this package was compiled against declares.
func Test_CheckABI_MatchesLinkedArchive(t *testing.T) {
	require.NoError(t, vxlan.CheckABI())
}

// Test_DeviceVxlanService_ShowDevice_UnknownName verifies that ShowDevice
// reports NotFound for a name absent from the dataplane device registry.
func Test_DeviceVxlanService_ShowDevice_UnknownName(t *testing.T) {
	service, _, _ := newVxlanService(t, "plain")

	resp, err := service.ShowDevice(t.Context(), &vxlanpb.ShowDeviceVxlanRequest{Name: "absent"})

	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_DeviceVxlanService_Validation_RejectsInvalidRequests verifies that
// an invalid request reaches the client as InvalidArgument before the
// service touches shared memory.
func Test_DeviceVxlanService_Validation_RejectsInvalidRequests(t *testing.T) {
	service, _, shm := newVxlanService(t, "plain")
	client := newVxlanClient(t, service)
	freeBefore := freeBytesForAgent(t, shm, "vxlan")

	updateCases := []struct {
		name    string
		request *vxlanpb.UpdateDeviceVxlanRequest
	}{
		{
			name:    "empty request",
			request: &vxlanpb.UpdateDeviceVxlanRequest{},
		},
		{
			name: "missing tunnel",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name:   "vx0",
				Device: &commonpb.Device{},
			},
		},
		{
			name: "multicast remote endpoint",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name:   "vx0",
				Device: &commonpb.Device{},
				Tunnel: func() *vxlanpb.VxlanTunnel {
					tunnel := validTunnel()
					tunnel.RemoteIp = commonpb.NewIPv4Address([4]byte{224, 0, 0, 1})
					return tunnel
				}(),
			},
		},
	}
	for _, tc := range updateCases {
		t.Run("update "+tc.name, func(t *testing.T) {
			resp, err := client.UpdateDevice(t.Context(), tc.request)

			require.Nil(t, resp)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}

	t.Run("show empty name", func(t *testing.T) {
		resp, err := client.ShowDevice(t.Context(), &vxlanpb.ShowDeviceVxlanRequest{})

		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	require.Equal(t, freeBefore, freeBytesForAgent(t, shm, "vxlan"))
}

// Test_DeviceVxlanService_UpdateDevice_MissingDeviceType verifies that a
// dataplane built without the vxlan device type refuses the update with
// FailedPrecondition and leaves the agent arena untouched.
func Test_DeviceVxlanService_UpdateDevice_MissingDeviceType(t *testing.T) {
	service, _, shm := newVxlanService(t, "plain")
	freeBefore := freeBytesForAgent(t, shm, "vxlan")

	resp, err := service.UpdateDevice(t.Context(), &vxlanpb.UpdateDeviceVxlanRequest{
		Name:   "vx0",
		Device: &commonpb.Device{},
		Tunnel: validTunnel(),
	})

	require.Nil(t, resp)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "device type 'vxlan' not found in dataplane config")
	require.Equal(t, freeBefore, freeBytesForAgent(t, shm, "vxlan"))
}

// Test_DeviceVxlanService_UpdateDevice_TunnelRulesMatchRustApi verifies that
// the Go tunnel rules and the Rust control-plane api accept the same
// tunnels.
//
// The service is called directly, so only the Rust api validates: an
// accepted tunnel proceeds to the missing device type, a rejected one stops
// at InvalidArgument first.
func Test_DeviceVxlanService_UpdateDevice_TunnelRulesMatchRustApi(t *testing.T) {
	service, _, _ := newVxlanService(t, "plain")

	cases := []struct {
		name   string
		mutate func(tunnel *vxlanpb.VxlanTunnel)
		valid  bool
	}{
		{
			name:   "valid tunnel",
			mutate: func(*vxlanpb.VxlanTunnel) {},
			valid:  true,
		},
		{
			name:   "VNI at maximum",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) { tunnel.Vni = 16777215 },
			valid:  true,
		},
		{
			name:   "VNI above maximum",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) { tunnel.Vni = 16777216 },
		},
		{
			name: "first address above multicast",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalIp = commonpb.NewIPv4Address([4]byte{240, 0, 0, 1})
			},
			valid: true,
		},
		{
			name: "last multicast address",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalIp = commonpb.NewIPv4Address([4]byte{239, 255, 255, 255})
			},
		},
		{
			name: "unspecified local IP",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalIp = commonpb.NewIPv4Address([4]byte{})
			},
		},
		{
			name: "broadcast remote IP",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.RemoteIp = commonpb.NewIPv4Address([4]byte{255, 255, 255, 255})
			},
		},
		{
			name: "zero local MAC",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalMac = commonpb.NewMACAddressEUI48([6]byte{})
			},
		},
		{
			name: "multicast remote MAC",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.RemoteMac = commonpb.NewMACAddressEUI48([6]byte{0x01, 0x00, 0x5e, 0, 0, 1})
			},
		},
		{
			name: "first unicast MAC",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.RemoteMac = commonpb.NewMACAddressEUI48([6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01})
			},
			valid: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tunnel := validTunnel()
			tc.mutate(tunnel)

			require.Equal(t, tc.valid, tunnel.Validate() == nil)

			_, err := service.UpdateDevice(t.Context(), &vxlanpb.UpdateDeviceVxlanRequest{
				Name:   "vx0",
				Device: &commonpb.Device{},
				Tunnel: tunnel,
			})

			want := codes.InvalidArgument
			if tc.valid {
				want = codes.FailedPrecondition
			}
			require.Equal(t, want, status.Code(err), "error: %v", err)
		})
	}
}
