package unrdup_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
	unrdup "github.com/yanet-platform/yanet2/modules/unrdup/controlplane"
	"github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

var errBackendFailure = errors.New("backend failure")

// fakeHandle is a shared-memory handle whose release always succeeds.
type fakeHandle struct{}

func (m *fakeHandle) Free() error {
	return nil
}

// fakeBackend counts updates, fails each with the set error, if any, and
// otherwise records the sources and services of the published config.
type fakeBackend struct {
	err      error
	calls    int
	sources  []xnetip.Network
	services []cunrdup.Service
}

func (m *fakeBackend) Update(name string, payload *unrdup.ModuleConfig) (crud.Handle, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}

	m.sources = payload.Sources()
	m.services = payload.Services
	return &fakeHandle{}, nil
}

func (m *fakeBackend) Delete(name string) error {
	return nil
}

// ipAddr returns the address message for valid address text.
func ipAddr(addr string) *commonpb.IPAddress {
	return commonpb.NewIPAddressFromAddr(netip.MustParseAddr(addr))
}

// ipv4Net returns the IPv4 network message for valid network text.
func ipv4Net(network string) *commonpb.IPv4Network {
	return commonpb.NewIPv4NetworkFrom4(xnetip.MustParseNetwork4(network))
}

// ipv6Net returns the IPv6 network message for valid network text.
func ipv6Net(network string) *commonpb.IPv6Network {
	return commonpb.NewIPv6NetworkFrom6(xnetip.MustParseNetwork6(network))
}

// validConfig returns a config with both source families and one TCP
// service that the service accepts.
func validConfig() *unrduppb.Config {
	return &unrduppb.Config{
		SourceV4: ipv4Net("10.0.0.1/32"),
		SourceV6: ipv6Net("2001:db8:a::/96"),
		Services: []*unrduppb.Service{
			{
				Vip:   ipAddr("192.0.2.1"),
				Peers: []*commonpb.IPAddress{ipAddr("10.0.0.10")},
				Endpoints: []*unrduppb.Endpoint{
					{Port: 443, Protocol: unrduppb.Protocol_PROTOCOL_TCP},
				},
			},
		},
	}
}

// Test_UnrdupService_UpdateConfig_PublishesConvertedConfig verifies that an
// accepted config reaches the backend with its sources and services
// converted.
func Test_UnrdupService_UpdateConfig_PublishesConvertedConfig(t *testing.T) {
	backend := &fakeBackend{}
	service := unrdup.NewUnrdupService(backend)

	_, err := service.UpdateConfig(t.Context(), &unrduppb.UpdateConfigRequest{
		Name:   "unrdup0",
		Config: validConfig(),
	})
	require.NoError(t, err)

	require.Equal(t, 1, backend.calls)
	require.Len(t, backend.sources, 2)
	require.Len(t, backend.services, 1)

	stored := backend.services[0]
	require.Equal(t, netip.MustParseAddr("192.0.2.1"), stored.VIP)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("10.0.0.10")}, stored.Peers)
	require.Equal(t, []cunrdup.Endpoint{{Port: 443, Proto: 6}}, stored.Endpoints)
}

// Test_UnrdupService_UpdateConfig_OneSourceFamily verifies that a config
// without an IPv6 source publishes a single source.
func Test_UnrdupService_UpdateConfig_OneSourceFamily(t *testing.T) {
	backend := &fakeBackend{}
	service := unrdup.NewUnrdupService(backend)

	config := validConfig()
	config.SourceV6 = nil

	_, err := service.UpdateConfig(t.Context(), &unrduppb.UpdateConfigRequest{
		Name:   "unrdup0",
		Config: config,
	})
	require.NoError(t, err)

	require.Len(t, backend.sources, 1)
}

// Test_UnrdupService_UpdateConfig_NoServices verifies that a config without
// services is accepted and published with an empty service list.
func Test_UnrdupService_UpdateConfig_NoServices(t *testing.T) {
	backend := &fakeBackend{}
	service := unrdup.NewUnrdupService(backend)

	config := validConfig()
	config.Services = nil

	_, err := service.UpdateConfig(t.Context(), &unrduppb.UpdateConfigRequest{
		Name:   "unrdup0",
		Config: config,
	})
	require.NoError(t, err)

	require.Equal(t, 1, backend.calls)
	require.Empty(t, backend.services)
}

// Test_UnrdupService_UpdateConfig_BothTransportsOnOnePort verifies that TCP
// and UDP endpoints may share a port within one service.
func Test_UnrdupService_UpdateConfig_BothTransportsOnOnePort(t *testing.T) {
	backend := &fakeBackend{}
	service := unrdup.NewUnrdupService(backend)

	config := validConfig()
	config.Services[0].Endpoints = append(
		config.Services[0].Endpoints,
		&unrduppb.Endpoint{
			Port:     443,
			Protocol: unrduppb.Protocol_PROTOCOL_UDP,
		},
	)

	_, err := service.UpdateConfig(t.Context(), &unrduppb.UpdateConfigRequest{
		Name:   "unrdup0",
		Config: config,
	})
	require.NoError(t, err)

	require.Len(t, backend.services[0].Endpoints, 2)
}

// Test_UnrdupService_UpdateConfig_RejectsInvalidConfig verifies that an
// invalid config fails with a flat InvalidArgument status and never reaches
// the backend.
func Test_UnrdupService_UpdateConfig_RejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name    string
		request *unrduppb.UpdateConfigRequest
	}{
		{
			name: "same vip and port in two services",
			request: withConfig(func(config *unrduppb.Config) {
				second := &unrduppb.Service{
					Vip:   ipAddr("192.0.2.1"),
					Peers: []*commonpb.IPAddress{ipAddr("10.0.0.11")},
					Endpoints: []*unrduppb.Endpoint{
						{
							Port:     443,
							Protocol: unrduppb.Protocol_PROTOCOL_UDP,
						},
					},
				}
				config.Services = append(config.Services, second)
			}),
		},
		{
			name: "vip unspecified",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services[0].Vip = ipAddr("0.0.0.0")
			}),
		},
		{
			name: "peer listed twice",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services[0].Peers = []*commonpb.IPAddress{
					ipAddr("10.0.0.10"),
					ipAddr("10.0.0.10"),
				}
			}),
		},
		{
			name: "peer address unspecified",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services[0].Peers = []*commonpb.IPAddress{
					ipAddr("0.0.0.0"),
				}
			}),
		},
		{
			name: "peer of a family with no source",
			request: withConfig(func(config *unrduppb.Config) {
				config.SourceV6 = nil
				config.Services[0].Peers = []*commonpb.IPAddress{
					ipAddr("2001:db8:b::11"),
				}
			}),
		},
		{
			name: "vip of the wrong length",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services[0].Vip = &commonpb.IPAddress{Addr: []byte{1, 2, 3}}
			}),
		},
		{
			name: "source address unspecified",
			request: withConfig(func(config *unrduppb.Config) {
				config.SourceV4 = ipv4Net("0.0.0.0/32")
			}),
		},
		{
			name: "source of the other family mapped into its field",
			request: withConfig(func(config *unrduppb.Config) {
				config.SourceV6 = ipv6Net("::ffff:10.0.0.0/120")
			}),
		},
		{
			name: "source mask scattered",
			request: withConfig(func(config *unrduppb.Config) {
				config.SourceV4 = ipv4Net("10.0.0.1/255.0.255.0")
			}),
		},
		{
			name: "source mask frees the whole address",
			request: withConfig(func(config *unrduppb.Config) {
				config.SourceV4 = ipv4Net("10.0.0.1/0.0.0.0")
			}),
		},
		{
			name: "source mask missing",
			request: withConfig(func(config *unrduppb.Config) {
				config.SourceV4 = &commonpb.IPv4Network{
					Addr: commonpb.NewIPv4Address([4]byte{10, 0, 0, 1}),
				}
			}),
		},
		{
			name: "endpoint served twice",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services = append(config.Services, &unrduppb.Service{
					Vip:   ipAddr("192.0.2.1"),
					Peers: []*commonpb.IPAddress{ipAddr("10.0.0.11")},
					Endpoints: []*unrduppb.Endpoint{
						{Port: 443, Protocol: unrduppb.Protocol_PROTOCOL_TCP},
					},
				})
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{}
			service := unrdup.NewUnrdupService(backend)

			_, err := service.UpdateConfig(t.Context(), tc.request)

			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.NotContains(t, status.Convert(err).Message(), "rpc error", "a status must not nest another")
			require.Zero(t, backend.calls, "nothing may reach shared memory")
		})
	}
}

// withConfig returns an update request carrying the valid test config after
// the given change.
func withConfig(mutate func(config *unrduppb.Config)) *unrduppb.UpdateConfigRequest {
	config := validConfig()
	mutate(config)

	return &unrduppb.UpdateConfigRequest{Name: "unrdup0", Config: config}
}

// Test_UnrdupService_UpdateConfig_BackendFailure verifies that a config the
// backend fails to publish reports Internal and is not stored.
func Test_UnrdupService_UpdateConfig_BackendFailure(t *testing.T) {
	backend := &fakeBackend{err: errBackendFailure}
	service := unrdup.NewUnrdupService(backend)

	_, err := service.UpdateConfig(t.Context(), &unrduppb.UpdateConfigRequest{
		Name:   "unrdup0",
		Config: validConfig(),
	})

	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))

	_, err = service.ShowConfig(t.Context(), &unrduppb.ShowConfigRequest{
		Name: "unrdup0",
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_UnrdupService_ShowConfig_RendersPublishedConfig verifies that a
// published config converts back to the same sources and services.
func Test_UnrdupService_ShowConfig_RendersPublishedConfig(t *testing.T) {
	backend := &fakeBackend{}
	service := unrdup.NewUnrdupService(backend)

	_, err := service.UpdateConfig(t.Context(), &unrduppb.UpdateConfigRequest{
		Name:   "unrdup0",
		Config: validConfig(),
	})
	require.NoError(t, err)

	response, err := service.ShowConfig(t.Context(), &unrduppb.ShowConfigRequest{
		Name: "unrdup0",
	})
	require.NoError(t, err)

	require.Equal(t, "unrdup0", response.GetName())

	config := response.GetConfig()
	sourceV4, err := config.GetSourceV4().ToNetwork4()
	require.NoError(t, err)
	require.Equal(t, xnetip.MustParseNetwork4("10.0.0.1/32"), sourceV4)
	sourceV6, err := config.GetSourceV6().ToNetwork6()
	require.NoError(t, err)
	require.Equal(t, xnetip.MustParseNetwork6("2001:db8:a::/96"), sourceV6)
	require.Len(t, config.GetServices(), 1)

	stored := config.GetServices()[0]
	require.Equal(t, ipAddr("192.0.2.1").GetAddr(), stored.GetVip().GetAddr())
	require.Len(t, stored.GetPeers(), 1)
	require.Equal(t, uint32(443), stored.GetEndpoints()[0].GetPort())
	require.Equal(
		t,
		unrduppb.Protocol_PROTOCOL_TCP,
		stored.GetEndpoints()[0].GetProtocol(),
	)
}

// Test_UnrdupService_ShowConfig_NotFound verifies that showing an unknown
// config reports NotFound.
func Test_UnrdupService_ShowConfig_NotFound(t *testing.T) {
	service := unrdup.NewUnrdupService(&fakeBackend{})

	_, err := service.ShowConfig(t.Context(), &unrduppb.ShowConfigRequest{
		Name: "missing",
	})

	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_UnrdupService_ListConfigs_Empty verifies that a fresh service lists
// no configs.
func Test_UnrdupService_ListConfigs_Empty(t *testing.T) {
	service := unrdup.NewUnrdupService(&fakeBackend{})

	response, err := service.ListConfigs(t.Context(), &unrduppb.ListConfigsRequest{})
	require.NoError(t, err)

	require.Empty(t, response.GetConfigs())
}

// Test_UnrdupService_DeleteConfig_MissingConfig verifies that deleting a
// config that was never created returns NotFound.
func Test_UnrdupService_DeleteConfig_MissingConfig(t *testing.T) {
	service := unrdup.NewUnrdupService(&fakeBackend{})

	response, err := service.DeleteConfig(t.Context(), &unrduppb.DeleteConfigRequest{
		Name: "unrdup0",
	})
	require.Nil(t, response)
	require.Equal(t, codes.NotFound, status.Code(err))
}
