package nat64s_test

import (
	"context"
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64s/controlplane"
	nat64spb "github.com/yanet-platform/yanet2/modules/nat64s/controlplane/nat64spb/v1"
)

// mockHandle is a shared-memory handle whose release always succeeds.
type mockHandle struct{}

func (m *mockHandle) Free() error {
	return nil
}

// mockBackend records every published config and finds nothing to delete
// in the dataplane.
type mockBackend struct {
	Configs []nat64s.NAT64sConfig
}

func (m *mockBackend) Update(name string, cfg *nat64s.NAT64sConfig) (crud.Handle, error) {
	m.Configs = append(m.Configs, *cfg)
	return &mockHandle{}, nil
}

func (m *mockBackend) Delete(name string) error {
	return ffi.ErrNotFound
}

// mustIPv6Prefix builds the wire prefix of valid text, panicking on a
// programming error in the test data.
func mustIPv6Prefix(value string) *commonpb.IPv6Prefix {
	prefix, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix(value))
	if err != nil {
		panic(err)
	}
	return prefix
}

// updateConfig serves an update as the gateway does, request validation
// ahead of the handler, and returns the status the caller would see.
func updateConfig(
	t *testing.T,
	service *nat64s.NAT64sService,
	request *nat64spb.UpdateConfigRequest,
) error {
	t.Helper()

	_, err := xgrpc.ValidateUnaryInterceptor()(
		t.Context(),
		request,
		&grpc.UnaryServerInfo{},
		func(ctx context.Context, message any) (any, error) {
			return service.UpdateConfig(ctx, request)
		},
	)
	return err
}

func validUpdate(name string) *nat64spb.UpdateConfigRequest {
	return &nat64spb.UpdateConfigRequest{
		Name: name,
		Config: &nat64spb.Config{
			Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix("64:ff9b::/96")},
			Pool:     []*commonpb.IPv4Address{{Addr: 0xc6336401}},
			Map:      "sessions",
		},
	}
}

// poolAddresses renders a stored pool the way shared memory holds it: the
// network-order bytes of each address.
func poolAddresses(pool []uint32) [][4]byte {
	addresses := make([][4]byte, 0, len(pool))
	for _, address := range pool {
		var raw [4]byte
		binary.NativeEndian.PutUint32(raw[:], address)
		addresses = append(addresses, raw)
	}
	return addresses
}

// Test_NAT64sService_UpdateConfig_AcceptsWholeConfig verifies the happy
// path: a whole config publishes and reads back through ShowConfig.
func Test_NAT64sService_UpdateConfig_AcceptsWholeConfig(t *testing.T) {
	backend := &mockBackend{}
	service := nat64s.NewNAT64sService(backend)
	ctx := t.Context()

	request := validUpdate("t0")
	request.Config.Timeout = 300
	request.Config.TcpEstablishedTimeout = 7440
	request.Config.PortRange = &nat64spb.PortRange{Lo: 1024, Hi: 4096}

	_, err := service.UpdateConfig(ctx, request)
	require.NoError(t, err)
	require.Len(t, backend.Configs, 1)

	published := backend.Configs[0]
	require.Equal(t, [][]byte{{0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0}}, published.Prefixes)
	require.Equal(t, [][4]byte{{198, 51, 100, 1}}, poolAddresses(published.Pool))
	require.Equal(t, uint16(300), published.Timeout.Timeout)
	require.Equal(t, uint16(7440), published.Timeout.TCPEstablished)
	require.Equal(t, "sessions", published.MapName)

	shown, err := service.ShowConfig(ctx, &nat64spb.ShowConfigRequest{Name: "t0"})
	require.NoError(t, err)
	require.Len(t, shown.GetConfig().GetPrefixes(), 1)
	shownPrefix, err := shown.GetConfig().GetPrefixes()[0].ToPrefix()
	require.NoError(t, err)
	require.Equal(t, netip.MustParsePrefix("64:ff9b::/96"), shownPrefix)
	require.Len(t, shown.GetConfig().GetPool(), 1)

	listed, err := service.ListConfigs(ctx, &nat64spb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"t0"}, listed.GetConfigs())
}

// Test_NAT64sService_UpdateConfig_StoresPoolInNetworkOrder verifies that a
// pool address reaches the dataplane as its own network-order bytes, the
// form packets are compared against, and reads back as the address sent.
func Test_NAT64sService_UpdateConfig_StoresPoolInNetworkOrder(t *testing.T) {
	backend := &mockBackend{}
	service := nat64s.NewNAT64sService(backend)

	request := validUpdate("t0")
	request.Config.Pool = []*commonpb.IPv4Address{
		{Addr: 0xc6336401},
		{Addr: 0x010203ff},
	}

	err := updateConfig(t, service, request)
	require.NoError(t, err)
	require.Equal(
		t,
		[][4]byte{{198, 51, 100, 1}, {1, 2, 3, 255}},
		poolAddresses(backend.Configs[0].Pool),
	)

	shown, err := service.ShowConfig(t.Context(), &nat64spb.ShowConfigRequest{Name: "t0"})
	require.NoError(t, err)
	require.Equal(
		t,
		[]uint32{0xc6336401, 0x010203ff},
		[]uint32{
			shown.GetConfig().GetPool()[0].GetAddr(),
			shown.GetConfig().GetPool()[1].GetAddr(),
		},
	)
}

// Test_NAT64sService_UpdateConfig_AcceptsShownConfig verifies that what a
// config left at its defaults shows is a valid update: the zero port
// window a shown default carries selects the default again instead of
// being refused.
func Test_NAT64sService_UpdateConfig_AcceptsShownConfig(t *testing.T) {
	backend := &mockBackend{}
	service := nat64s.NewNAT64sService(backend)
	ctx := t.Context()

	_, err := service.UpdateConfig(ctx, validUpdate("t0"))
	require.NoError(t, err)

	shown, err := service.ShowConfig(ctx, &nat64spb.ShowConfigRequest{Name: "t0"})
	require.NoError(t, err)
	require.NotNil(t, shown.GetConfig().GetPortRange())

	err = updateConfig(
		t,
		service,
		&nat64spb.UpdateConfigRequest{Name: "t0", Config: shown.GetConfig()},
	)
	require.NoError(t, err)
	require.Equal(t, backend.Configs[0], backend.Configs[1])
}

// Test_NAT64sService_UpdateConfig_RejectsBadConfigs verifies that nothing
// publishes when the whole config is invalid.
func Test_NAT64sService_UpdateConfig_RejectsBadConfigs(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*nat64spb.UpdateConfigRequest)
		message string
	}{
		{
			name:    "missing config",
			mutate:  func(r *nat64spb.UpdateConfigRequest) { r.Config = nil },
			message: "config is required",
		},
		{
			name: "wrong prefix length",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Prefixes = []*commonpb.IPv6Prefix{mustIPv6Prefix("2001:db8::/64")}
			},
			message: "prefix must be a /96",
		},
		{
			name: "prefix listed twice",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Prefixes = []*commonpb.IPv6Prefix{
					mustIPv6Prefix("64:ff9b::/96"),
					mustIPv6Prefix("64:ff9b::/96"),
				}
			},
			message: "prefix is listed twice",
		},
		{
			name: "pool address twice",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Pool = append(r.Config.Pool, &commonpb.IPv4Address{Addr: 0xc6336401})
			},
			message: "listed twice",
		},
		{
			name: "empty port range",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.PortRange = &nat64spb.PortRange{Lo: 4096, Hi: 1024}
			},
			message: "port range must be non-empty",
		},
		{
			name:    "no session map",
			mutate:  func(r *nat64spb.UpdateConfigRequest) { r.Config.Map = "" },
			message: "must link its nat64state_map session tables",
		},
		{
			name:    "no config name",
			mutate:  func(r *nat64spb.UpdateConfigRequest) { r.Name = "" },
			message: "name is required",
		},
		{
			name: "ipv4 mtu below the minimum",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Mtu = &nat64spb.MTUConfig{Ipv4Mtu: 67}
			},
			message: "ipv4_mtu 67 is below the IPv4 minimum",
		},
		{
			name: "ipv6 mtu below the minimum",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Mtu = &nat64spb.MTUConfig{Ipv6Mtu: 1279}
			},
			message: "ipv6_mtu 1279 is below the IPv6 minimum",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := &mockBackend{}
			service := nat64s.NewNAT64sService(backend)

			request := validUpdate("t0")
			tc.mutate(request)
			err := updateConfig(t, service, request)
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Contains(t, err.Error(), tc.message)
			require.Empty(t, backend.Configs)
		})
	}
}

// Test_NAT64sService_Requests_RefuseMissingName verifies that a show,
// update or delete request that names no config is refused before it
// reaches the service.
func Test_NAT64sService_Requests_RefuseMissingName(t *testing.T) {
	validate := xgrpc.ValidateUnaryInterceptor()

	cases := []struct {
		name    string
		request interface{ Validate() error }
	}{
		{name: "show", request: &nat64spb.ShowConfigRequest{}},
		{name: "update", request: validUpdate("")},
		{name: "delete", request: &nat64spb.DeleteConfigRequest{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			_, err := validate(
				t.Context(),
				tc.request,
				&grpc.UnaryServerInfo{},
				func(ctx context.Context, message any) (any, error) {
					reached = true
					return nil, nil
				},
			)
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.False(
				t,
				reached,
				"a request naming no config must not reach the service",
			)
		})
	}
}

// Test_NAT64sService_DeleteConfig_MissingConfig verifies that deleting a
// config that was never created returns NotFound.
func Test_NAT64sService_DeleteConfig_MissingConfig(t *testing.T) {
	service := nat64s.NewNAT64sService(&mockBackend{})
	ctx := t.Context()

	resp, err := service.DeleteConfig(ctx, &nat64spb.DeleteConfigRequest{Name: "t0"})
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}
