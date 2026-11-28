package nat64s_test

import (
	"context"
	"encoding/binary"
	"fmt"
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
	"github.com/yanet-platform/yanet2/modules/nat64s/bindings/go/cnat64s"
	"github.com/yanet-platform/yanet2/modules/nat64s/controlplane"
	nat64spb "github.com/yanet-platform/yanet2/modules/nat64s/controlplane/nat64spb/v1"
	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

// sessionStream collects what a ShowSessions handler sends down a stream.
type sessionStream struct {
	nat64spb.NAT64SService_ShowSessionsServer

	ctx     context.Context
	Pages   []*nat64spb.SessionsPage
	SendErr error
	Sent    int
	// Request is what the handler's one receive decodes.
	Request *nat64spb.ShowSessionsRequest
	RecvErr error
}

func (m *sessionStream) Context() context.Context { return m.ctx }

func (m *sessionStream) RecvMsg(message any) error {
	if m.RecvErr != nil {
		return m.RecvErr
	}
	request, ok := message.(*nat64spb.ShowSessionsRequest)
	if !ok {
		return fmt.Errorf("unexpected request type %T", message)
	}
	request.Name = m.Request.GetName()
	request.PageSize = m.Request.GetPageSize()
	return nil
}

func (m *sessionStream) Send(page *nat64spb.SessionsPage) error {
	if m.SendErr != nil {
		return m.SendErr
	}
	m.Pages = append(m.Pages, page)
	m.Sent++
	return nil
}

// testSession builds one session as the binding would read it.
func testSession(clientPort, externalPort uint16) cnat64s.Session {
	session := cnat64s.Session{
		Proto:        6,
		ClientPort:   clientPort,
		RemotePort:   443,
		ExternalPort: externalPort,
		Established:  true,
		CreatedAt:    10,
		UpdatedAt:    20,
		LastTTLNs:    30,
		ExpiresAt:    50,
	}
	session.ClientAddr[15] = 1
	copy(session.RemoteAddr[:], []byte{0, 0x64, 0xff, 0x9b})
	session.RemoteAddr[15] = 34
	// 198.51.100.1 in network byte order.
	copy(session.ExternalAddr[:], []byte{198, 51, 100, 1})
	return session
}

// mockHandle is a shared-memory handle whose release always succeeds.
type mockHandle struct{}

func (m *mockHandle) Free() error {
	return nil
}

// mockBackend records every published config and finds nothing to delete
// in the dataplane.
type mockBackend struct {
	Configs []nat64s.NAT64sConfig
	// SessionPages are returned in order by Sessions, one per call; a
	// dump past the last page reads an empty page.
	SessionPages [][]cnat64s.Session
	// SessionCalls records the cursor of each Sessions call.
	SessionCalls []int64
	// SessionNow records the instant the last Sessions call was given.
	SessionNow uint64
	// SessionNows records the instant every Sessions call was given.
	SessionNows []uint64
	// Running is whether DataplaneTime reports a clock.
	Running bool
	// ClockStep is added to the reported instant on every read after the
	// first: a caller that must judge everything at one instant has to
	// read the clock once.
	ClockStep  uint64
	clockReads int
}

func (m *mockBackend) Update(name string, cfg *nat64s.NAT64sConfig) (crud.Handle, error) {
	m.Configs = append(m.Configs, *cfg)
	return &mockHandle{}, nil
}

func (m *mockBackend) Delete(name string) error {
	return ffi.ErrNotFound
}

func (m *mockBackend) Sessions(
	name string, cursor int64, now uint64, count uint32,
) ([]cnat64s.Session, int64, error) {
	m.SessionCalls = append(m.SessionCalls, cursor)
	m.SessionNow = now
	m.SessionNows = append(m.SessionNows, now)
	if len(m.SessionPages) == 0 {
		return nil, cursor, nil
	}
	page := m.SessionPages[0]
	m.SessionPages = m.SessionPages[1:]
	return page, cursor + int64(len(page)), nil
}

func (m *mockBackend) DataplaneTime() (uint64, bool) {
	now := uint64(42) + uint64(m.clockReads)*m.ClockStep
	m.clockReads++
	return now, m.Running
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
	request.Config.TcpTransitoryTimeout = 240
	request.Config.PortRange = &nat64spb.PortRange{Lo: 1024, Hi: 4096}

	_, err := service.UpdateConfig(ctx, request)
	require.NoError(t, err)
	require.Len(t, backend.Configs, 1)

	published := backend.Configs[0]
	require.Equal(t, [][]byte{{0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0}}, published.Prefixes)
	require.Equal(t, [][4]byte{{198, 51, 100, 1}}, poolAddresses(published.Pool))
	require.Equal(t, uint16(300), published.Timeout.Timeout)
	require.Equal(t, uint16(7440), published.Timeout.TCPEstablished)
	require.Equal(t, uint16(240), published.Timeout.TCPTransitory)
	require.Equal(t, "sessions", published.MapName)

	shown, err := service.ShowConfig(ctx, &nat64spb.ShowConfigRequest{Name: "t0"})
	require.NoError(t, err)
	require.Len(t, shown.GetConfig().GetPrefixes(), 1)
	shownPrefix, err := shown.GetConfig().GetPrefixes()[0].ToPrefix()
	require.NoError(t, err)
	require.Equal(t, netip.MustParsePrefix("64:ff9b::/96"), shownPrefix)
	require.Len(t, shown.GetConfig().GetPool(), 1)
	// The whole timeout set reads back as it was published.
	require.Equal(t, uint32(300), shown.GetConfig().GetTimeout())
	require.Equal(t, uint32(7440), shown.GetConfig().GetTcpEstablishedTimeout())
	require.Equal(t, uint32(240), shown.GetConfig().GetTcpTransitoryTimeout())

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
	request.Config.TcpTransitoryTimeout = nat64spb.MaxTimeout

	require.NoError(t, updateConfig(t, service, request))
	require.Len(t, backend.Configs, 1)
	require.Equal(t, uint16(nat64spb.MaxTimeout), backend.Configs[0].Timeout.Timeout)
	require.Equal(t, uint16(nat64spb.MaxTimeout), backend.Configs[0].Timeout.TCPEstablished)
	require.Equal(t, uint16(nat64spb.MaxTimeout), backend.Configs[0].Timeout.TCPTransitory)
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
			name: "transitory timeout above the largest one",
			mutate: func(r *nat64spb.UpdateConfigRequest) {
				r.Config.TcpTransitoryTimeout = nat64spb.MaxTimeout + 1
			},
			message: "tcp_transitory_timeout 65536 exceeds the maximum",
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

// publishedService returns a service with one published config, which a
// session dump needs to name.
func publishedService(t *testing.T, backend *mockBackend) *nat64s.NAT64sService {
	t.Helper()
	service := nat64s.NewNAT64sService(backend)
	require.NoError(t, updateConfig(t, service, validUpdate("t0")))
	return service
}

// Test_NAT64sService_ShowSessions_Pages verifies that a dump sends the
// session pages in order and ends on the first empty page.
func Test_NAT64sService_ShowSessions_Pages(t *testing.T) {
	backend := &mockBackend{Running: true}
	closed := testSession(2, 1002)
	closed.Established = false
	closed.ClientClosed = true
	closed.RemoteClosed = true
	backend.SessionPages = [][]cnat64s.Session{
		{testSession(1, 1001), closed},
		{testSession(3, 1003)},
	}
	service := publishedService(t, backend)

	stream := &sessionStream{ctx: t.Context()}
	err := service.ShowSessions(&nat64spb.ShowSessionsRequest{Name: "t0"}, stream)
	require.NoError(t, err)

	// Two pages, then the empty one that ends the stream is not sent.
	require.Len(t, stream.Pages, 2)
	require.Len(t, stream.Pages[0].Sessions, 2)
	require.Len(t, stream.Pages[1].Sessions, 1)

	// Every page carries the instant its sessions were judged live at.
	require.Equal(t, uint64(42), stream.Pages[0].GetNow())
	require.Equal(t, uint64(42), stream.Pages[1].GetNow())

	// The walk resumes after what each page returned.
	require.Equal(t, []int64{0, 2, 3}, backend.SessionCalls)

	// Every session crosses the wire whole, addresses in network order.
	first := stream.Pages[0].Sessions[0]
	require.Equal(t, uint32(6), first.Proto)
	require.Equal(t, uint32(1), first.ClientPort)
	require.Equal(t, uint32(443), first.RemotePort)
	require.Equal(t, uint32(1001), first.ExternalPort)
	require.True(t, first.Established)
	require.False(t, first.ClientClosed)
	require.False(t, first.RemoteClosed)
	require.Equal(t, uint64(10), first.CreatedAt)
	require.Equal(t, uint64(50), first.ExpiresAt)

	// Both closing flags cross as set, on a session that is not
	// established.
	second := stream.Pages[0].Sessions[1]
	require.False(t, second.Established)
	require.True(t, second.ClientClosed)
	require.True(t, second.RemoteClosed)

	client := netip.AddrFrom16([16]byte(first.ClientAddr.Addr))
	require.Equal(t, netip.MustParseAddr("::1"), client)
	remote := netip.AddrFrom16([16]byte(first.RemoteAddr.Addr))
	require.Equal(t, netip.MustParseAddr("64:ff9b::22"), remote)
	var external4 [4]byte
	binary.BigEndian.PutUint32(external4[:], first.ExternalAddr.Addr)
	require.Equal(t, netip.MustParseAddr("198.51.100.1"), netip.AddrFrom4(external4))
}

// Test_NAT64sService_ShowSessions_JudgesAtOneInstant verifies that every
// page of one dump is read at the same dataplane instant, so a session
// does not flicker between live and expired as the dump runs.
func Test_NAT64sService_ShowSessions_JudgesAtOneInstant(t *testing.T) {
	backend := &mockBackend{Running: true}
	backend.ClockStep = 7
	backend.SessionPages = [][]cnat64s.Session{
		{testSession(1, 1001)},
		{testSession(2, 1002)},
	}
	service := publishedService(t, backend)

	stream := &sessionStream{ctx: t.Context()}
	require.NoError(
		t,
		service.ShowSessions(&nat64spb.ShowSessionsRequest{Name: "t0"}, stream),
	)

	// Three reads — two pages and the empty one that ends the stream —
	// each at the instant of a single look at the clock, on a clock that
	// would have reported three different instants.
	require.Equal(t, []uint64{42, 42, 42}, backend.SessionNows)
	require.Len(t, stream.Pages, 2)
	for _, page := range stream.Pages {
		require.Equal(t, uint64(42), page.GetNow())
	}
	require.Equal(t, 1, backend.clockReads, "the clock must be read once")
}

// Test_NAT64sService_UpdateConfigRequest_RefusesKnobAtBoundary verifies
// that the request boundary itself refuses a session knob beyond what the
// dataplane holds, before the config is decoded.
func Test_NAT64sService_UpdateConfigRequest_RefusesKnobAtBoundary(t *testing.T) {
	request := validUpdate("t0")
	request.Config.TcpTransitoryTimeout = nat64spb.MaxTimeout + 1

	reached := false
	_, err := xgrpc.ValidateUnaryInterceptor()(
		t.Context(),
		request,
		&grpc.UnaryServerInfo{},
		func(ctx context.Context, message any) (any, error) {
			reached = true
			return nil, nil
		},
	)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(
		t,
		err.Error(),
		"tcp_transitory_timeout 65536 exceeds the maximum of 65535 seconds",
	)
	require.False(
		t,
		reached,
		"a knob beyond what the dataplane holds must not reach the service",
	)
}

// Test_NAT64sService_ShowSessions_RefuseMissingName verifies that a
// sessions request that names no config is refused before it reaches the
// service.
func Test_NAT64sService_ShowSessions_RefuseMissingName(t *testing.T) {
	backend := &mockBackend{Running: true}
	service := nat64s.NewNAT64sService(backend)
	stream := &sessionStream{
		ctx:     t.Context(),
		Request: &nat64spb.ShowSessionsRequest{Name: ""},
	}
	reached := false
	// A stream handler receives its request first, as the generated one
	// does; the validating stream checks what it decoded.
	handler := func(srv any, ss grpc.ServerStream) error {
		request := &nat64spb.ShowSessionsRequest{}
		if err := ss.RecvMsg(request); err != nil {
			return err
		}
		reached = true
		return srv.(nat64spb.NAT64SServiceServer).ShowSessions(
			request,
			ss.(nat64spb.NAT64SService_ShowSessionsServer),
		)
	}
	err := xgrpc.ValidateStreamInterceptor()(
		service,
		stream,
		&grpc.StreamServerInfo{
			FullMethod: nat64spb.NAT64SService_ShowSessions_FullMethodName,
		},
		handler,
	)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.False(
		t,
		reached,
		"a request naming no config must not reach the service",
	)
	require.Empty(t, backend.SessionCalls)
	require.Empty(t, stream.Pages)
}

// Test_NAT64sService_ShowSessions_StalledWalk verifies that a walk whose
// cursor stops advancing ends the stream instead of looping.
func Test_NAT64sService_ShowSessions_StalledWalk(t *testing.T) {
	backend := &mockBackend{Running: true}
	service := publishedService(t, backend)

	// A backend that always answers the same non-empty page from the
	// same cursor.
	backend.SessionPages = nil
	stall := &stalledBackend{mockBackend: backend}
	stall.SessionPages = [][]cnat64s.Session{{testSession(1, 1001)}}
	service = nat64s.NewNAT64sService(stall)
	require.NoError(t, updateConfig(t, service, validUpdate("t0")))

	stream := &sessionStream{ctx: t.Context()}
	require.NoError(
		t,
		service.ShowSessions(&nat64spb.ShowSessionsRequest{Name: "t0"}, stream),
	)
	require.Equal(t, 1, stream.Sent)
}

// stalledBackend answers every Sessions call with the same page and a
// cursor that never moves.
type stalledBackend struct {
	*mockBackend
	SessionPages [][]cnat64s.Session
}

func (m *stalledBackend) Sessions(
	name string, cursor int64, now uint64, count uint32,
) ([]cnat64s.Session, int64, error) {
	return m.SessionPages[0], cursor, nil
}

// Test_NAT64sService_ShowSessions_Rejections verifies the failures that
// end a dump before its first page.
func Test_NAT64sService_ShowSessions_Rejections(t *testing.T) {
	tests := []struct {
		name    string
		backend *mockBackend
		request *nat64spb.ShowSessionsRequest
		code    codes.Code
	}{
		{
			name:    "unknown config",
			backend: &mockBackend{Running: true},
			request: &nat64spb.ShowSessionsRequest{Name: "nope"},
			code:    codes.NotFound,
		},
		{
			name: "dataplane not running",
			backend: func() *mockBackend {
				b := &mockBackend{}
				b.Configs = []nat64s.NAT64sConfig{{MapName: "sessions"}}
				return b
			}(),
			request: &nat64spb.ShowSessionsRequest{Name: "t0"},
			code:    codes.Unavailable,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var service *nat64s.NAT64sService
			if tc.backend.Running || tc.request.GetName() == "nope" {
				service = nat64s.NewNAT64sService(tc.backend)
			} else {
				// A published config the running state does not
				// matter for: publish through a running backend,
				// then report the dataplane down.
				tc.backend.Running = true
				service = nat64s.NewNAT64sService(tc.backend)
				require.NoError(t, updateConfig(t, service, validUpdate("t0")))
				tc.backend.Running = false
			}
			stream := &sessionStream{ctx: t.Context()}
			err := service.ShowSessions(tc.request, stream)
			require.Equal(t, tc.code, status.Code(err))
			require.Empty(t, stream.Pages)
		})
	}
}

// Test_NAT64sService_ShowSessions_Cancelled verifies that a stream whose
// context is cancelled ends with the context's error.
func Test_NAT64sService_ShowSessions_Cancelled(t *testing.T) {
	backend := &mockBackend{Running: true}
	service := publishedService(t, backend)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stream := &sessionStream{ctx: ctx}
	err := service.ShowSessions(&nat64spb.ShowSessionsRequest{Name: "t0"}, stream)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, stream.Pages)
}
