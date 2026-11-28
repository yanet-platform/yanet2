package nat64s_test

import (
	"context"
	"encoding/binary"
	"net/netip"
	"strings"
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
	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
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

// poolAddress replaces a request's pool with the one address, given as
// its wire form, a big-endian integer.
func poolAddress(address uint32) func(*nat64spb.UpdateConfigRequest) {
	return func(request *nat64spb.UpdateConfigRequest) {
		request.Config.Pool = []*commonpb.IPv4Address{{Addr: address}}
	}
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

// Test_NAT64sService_UpdateConfig_KeepsLargestTimeoutsWhole verifies that
// the largest timeouts a knob represents pass the request boundary and
// reach the stored config as they were sent.
func Test_NAT64sService_UpdateConfig_KeepsLargestTimeoutsWhole(t *testing.T) {
	backend := &mockBackend{}
	service := nat64s.NewNAT64sService(backend)

	request := validUpdate("t0")
	request.Config.Timeout = nat64spb.MaxTimeout
	request.Config.TcpEstablishedTimeout = nat64spb.MaxTimeout

	require.NoError(t, updateConfig(t, service, request))
	require.Len(t, backend.Configs, 1)
	require.Equal(t, uint16(nat64spb.MaxTimeout), backend.Configs[0].Timeout.Timeout)
	require.Equal(t, uint16(nat64spb.MaxTimeout), backend.Configs[0].Timeout.TCPEstablished)
}

// Test_NAT64sService_UpdateConfig_KeepsWindowEndingWithLastPort verifies
// that a translated-port window ending with port 65535, whose exclusive
// upper bound is one past a 16-bit port, is stored and shown as it was
// sent.
func Test_NAT64sService_UpdateConfig_KeepsWindowEndingWithLastPort(t *testing.T) {
	backend := &mockBackend{}
	service := nat64s.NewNAT64sService(backend)

	request := validUpdate("t0")
	request.Config.PortRange = &nat64spb.PortRange{Lo: 1024, Hi: 65536}

	require.NoError(t, updateConfig(t, service, request))
	require.Len(t, backend.Configs, 1)
	require.Equal(t, nat64s.PortRange{Lo: 1024, Hi: 65536}, backend.Configs[0].PortRange)

	shown, err := service.ShowConfig(t.Context(), &nat64spb.ShowConfigRequest{Name: "t0"})
	require.NoError(t, err)
	require.Equal(t, uint32(65536), shown.GetConfig().GetPortRange().GetHi())
}

// Test_NAT64sService_UpdateConfig_KeepsLargestIPv6Mtu verifies that the
// largest IPv6 MTU limit the translation carries is stored as it was
// sent.
func Test_NAT64sService_UpdateConfig_KeepsLargestIPv6Mtu(t *testing.T) {
	backend := &mockBackend{}
	service := nat64s.NewNAT64sService(backend)

	request := validUpdate("t0")
	request.Config.Mtu = &nat64spb.MTUConfig{
		Ipv4Mtu: 1500,
		Ipv6Mtu: nat64spb.MaxIPv6MTU,
	}

	require.NoError(t, updateConfig(t, service, request))
	require.Len(t, backend.Configs, 1)
	require.Equal(t, uint32(1500), backend.Configs[0].MTU.IPv4MTU)
	require.Equal(t, uint32(nat64spb.MaxIPv6MTU), backend.Configs[0].MTU.IPv6MTU)
}

// Test_NAT64sService_UpdateConfig_KeepsSmallestMtu verifies that an MTU
// limit at its family's minimum is stored as it was sent: the smallest
// one every link of that family must take.
func Test_NAT64sService_UpdateConfig_KeepsSmallestMtu(t *testing.T) {
	backend := &mockBackend{}
	service := nat64s.NewNAT64sService(backend)

	request := validUpdate("t0")
	request.Config.Mtu = &nat64spb.MTUConfig{Ipv4Mtu: 68, Ipv6Mtu: 1280}

	require.NoError(t, updateConfig(t, service, request))
	require.Len(t, backend.Configs, 1)
	require.Equal(t, uint32(68), backend.Configs[0].MTU.IPv4MTU)
	require.Equal(t, uint32(1280), backend.Configs[0].MTU.IPv6MTU)
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
			name:    "pool address unspecified",
			mutate:  poolAddress(0x00000000),
			message: "pool[0]: 0.0.0.0 is not a unicast address of a single host",
		},
		{
			name:    "pool address of a whole network",
			mutate:  poolAddress(0x00010203),
			message: "pool[0]: 0.1.2.3 is not a unicast address of a single host",
		},
		{
			name:    "pool address loopback",
			mutate:  poolAddress(0x7f000001),
			message: "pool[0]: 127.0.0.1 is not a unicast address of a single host",
		},
		{
			name:    "pool address multicast",
			mutate:  poolAddress(0xe0000001),
			message: "pool[0]: 224.0.0.1 is not a unicast address of a single host",
		},
		{
			name:    "pool address reserved",
			mutate:  poolAddress(0xf0000001),
			message: "pool[0]: 240.0.0.1 is not a unicast address of a single host",
		},
		{
			name:    "pool address limited broadcast",
			mutate:  poolAddress(0xffffffff),
			message: "pool[0]: 255.255.255.255 is not a unicast address of a single host",
		},
		{
			name:    "pool address link-local",
			mutate:  poolAddress(0xa9fe0001),
			message: "pool[0]: 169.254.0.1 is not a unicast address of a single host",
		},
		{
			name: "empty port range",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.PortRange = &nat64spb.PortRange{Lo: 4096, Hi: 1024}
			},
			message: "port range must be non-empty",
		},
		{
			name: "port range upper bound past the last port",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.PortRange = &nat64spb.PortRange{Lo: 1024, Hi: 65537}
			},
			message: "port range must lie within [0, 65536)",
		},
		{
			name: "port range lower bound past the last port",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.PortRange = &nat64spb.PortRange{Lo: 65536, Hi: 65536}
			},
			message: "port range must lie within [0, 65536)",
		},
		{
			name:    "no session map",
			mutate:  func(r *nat64spb.UpdateConfigRequest) { r.Config.Map = "" },
			message: "map is required",
		},
		{
			name: "session map name with a NUL byte",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Map = "sessions\x00elsewhere"
			},
			message: "map must not contain NUL",
		},
		{
			name: "session map name as long as the object name buffer",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Map = strings.Repeat("s", fwstatemappb.MaxMapNameLen)
			},
			message: "map must be shorter than",
		},
		{
			name:    "timeout above the largest one",
			mutate:  func(r *nat64spb.UpdateConfigRequest) { r.Config.Timeout = nat64spb.MaxTimeout + 1 },
			message: "timeout 65536 exceeds the maximum of 65535 seconds",
		},
		{
			name: "established timeout above the largest one",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.TcpEstablishedTimeout = nat64spb.MaxTimeout + 1
			},
			message: "tcp_established_timeout 65536 exceeds the maximum",
		},
		{
			name:    "no config name",
			mutate:  func(r *nat64spb.UpdateConfigRequest) { r.Name = "" },
			message: "name is required",
		},
		{
			name: "multicast prefix",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Prefixes = []*commonpb.IPv6Prefix{
					mustIPv6Prefix("ff0e::/96"),
				}
			},
			message: "prefixes[0]: ff0e::/96 is not a unicast prefix",
		},
		{
			name: "link-local prefix",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Prefixes = []*commonpb.IPv6Prefix{
					mustIPv6Prefix("fe80::/96"),
				}
			},
			message: "prefixes[0]: fe80::/96 is not a unicast prefix",
		},
		{
			name: "IPv4-mapped prefix",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Prefixes = []*commonpb.IPv6Prefix{
					mustIPv6Prefix("::ffff:0:0/96"),
				}
			},
			message: "prefixes[0]: ::ffff:0.0.0.0/96 is not a unicast prefix",
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
		{
			name: "ipv6 mtu above the largest limit",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Mtu = &nat64spb.MTUConfig{Ipv6Mtu: 32768}
			},
			message: "ipv6_mtu 32768 exceeds the largest limit of 32767",
		},
		{
			name: "ipv4 mtu above the largest limit",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.Mtu = &nat64spb.MTUConfig{Ipv4Mtu: 65536}
			},
			message: "ipv4_mtu 65536 exceeds the largest limit of 65535",
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

// Test_NAT64sService_UpdateConfig_DecodeJudgesMapName verifies that a
// config decoded without the request boundary ahead of it is held to the
// map-name rule all the same.
func Test_NAT64sService_UpdateConfig_DecodeJudgesMapName(t *testing.T) {
	backend := &mockBackend{}
	service := nat64s.NewNAT64sService(backend)

	request := validUpdate("t0")
	request.Config.Map = "sessions\x00elsewhere"
	_, err := service.UpdateConfig(t.Context(), request)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Empty(t, backend.Configs)
}

// Test_NAT64sService_UpdateConfig_AcceptsPrivateAndSharedPool verifies
// that pool addresses of the private and shared blocks stay allowed: a
// deployment under a network-specific prefix may translate to them.
func Test_NAT64sService_UpdateConfig_AcceptsPrivateAndSharedPool(t *testing.T) {
	backend := &mockBackend{}
	service := nat64s.NewNAT64sService(backend)

	// 10.0.0.1 of the private block and 100.64.0.1 of the shared one.
	request := validUpdate("t0")
	request.Config.Pool = []*commonpb.IPv4Address{{Addr: 0x0a000001}, {Addr: 0x64400001}}

	require.NoError(t, updateConfig(t, service, request))
	require.Len(t, backend.Configs, 1)
	require.Len(t, backend.Configs[0].Pool, 2)
}

// Test_NAT64sService_UpdateConfig_DecodeJudgesMtu verifies that a config
// decoded without the request boundary ahead of it is held to the MTU
// limits all the same: each one stays zero or within what its family's
// link must take and its field or the translation can carry.
func Test_NAT64sService_UpdateConfig_DecodeJudgesMtu(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mtu     *nat64spb.MTUConfig
		message string
	}{
		{
			name:    "ipv4 above the largest limit",
			mtu:     &nat64spb.MTUConfig{Ipv4Mtu: 65536},
			message: "ipv4_mtu 65536 exceeds the largest limit of 65535",
		},
		{
			name:    "ipv6 above the largest limit",
			mtu:     &nat64spb.MTUConfig{Ipv6Mtu: 40000},
			message: "ipv6_mtu 40000 exceeds the largest limit of 32767",
		},
		{
			name:    "ipv4 below the minimum",
			mtu:     &nat64spb.MTUConfig{Ipv4Mtu: 67},
			message: "ipv4_mtu 67 is below the IPv4 minimum of 68",
		},
		{
			name:    "ipv6 below the minimum",
			mtu:     &nat64spb.MTUConfig{Ipv6Mtu: 1},
			message: "ipv6_mtu 1 is below the IPv6 minimum of 1280",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &mockBackend{}
			service := nat64s.NewNAT64sService(backend)

			request := validUpdate("t0")
			request.Config.Mtu = tc.mtu
			_, err := service.UpdateConfig(t.Context(), request)
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Contains(t, err.Error(), tc.message)
			require.Empty(t, backend.Configs)
		})
	}
}

// Test_NAT64sService_UpdateConfig_DecodeRefusesLargeTimeouts verifies
// that a config decoded without the request boundary ahead of it refuses
// a timeout the dataplane cannot hold, instead of narrowing it.
func Test_NAT64sService_UpdateConfig_DecodeRefusesLargeTimeouts(t *testing.T) {
	backend := &mockBackend{}
	service := nat64s.NewNAT64sService(backend)

	request := validUpdate("t0")
	request.Config.Timeout = nat64spb.MaxTimeout + 1
	_, err := service.UpdateConfig(t.Context(), request)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Empty(t, backend.Configs)
}

// Test_NAT64sService_Mutations_RefuseBadNameInProcess verifies that an
// update or a delete handed to the service in process, past the validating
// interceptor, still refuses a name the dataplane cannot carry whole: one
// with a NUL byte, or as long as the module layer's name buffer. That
// layer would cut either short, so two such names would list apart while
// replacing one config.
func Test_NAT64sService_Mutations_RefuseBadNameInProcess(t *testing.T) {
	names := map[string]string{
		"nul byte": "a\x00b",
		"too long": strings.Repeat("n", commonpb.MaxModuleNameLen),
	}
	for name, value := range names {
		t.Run(name, func(t *testing.T) {
			backend := &mockBackend{}
			service := nat64s.NewNAT64sService(backend)

			_, err := service.UpdateConfig(t.Context(), validUpdate(value))
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Empty(
				t,
				backend.Configs,
				"a name the dataplane cannot carry whole must publish nothing",
			)

			_, err = service.DeleteConfig(
				t.Context(),
				&nat64spb.DeleteConfigRequest{Name: value},
			)
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
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
