package unrdup_test

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
	unrdup "github.com/yanet-platform/yanet2/modules/unrdup/controlplane"
	"github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

var errBackendFailure = errors.New("backend failure")

type fakeHandle struct {
	freed bool
}

func (m *fakeHandle) Free() error {
	m.freed = true
	return nil
}

type fakeBackend struct {
	err      error
	calls    int
	handles  []*fakeHandle
	sources  []xnetip.Network
	services []cunrdup.Service
}

func (m *fakeBackend) UpdateModule(
	name string,
	sources []xnetip.Network,
	services []cunrdup.Service,
) (unrdup.ModuleHandle, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}

	m.sources = sources
	m.services = services

	handle := &fakeHandle{}
	m.handles = append(m.handles, handle)

	return handle, nil
}

func ipAddr(addr string) *commonpb.IPAddress {
	return commonpb.NewIPAddressFromAddr(netip.MustParseAddr(addr))
}

func ipNet(prefix string) *filterpb.IPNet {
	parsed := netip.MustParsePrefix(prefix)
	addr := parsed.Addr()

	return &filterpb.IPNet{
		Addr: addr.AsSlice(),
		Mask: net.CIDRMask(parsed.Bits(), addr.BitLen()),
	}
}

func validConfig() *unrduppb.Config {
	return &unrduppb.Config{
		SourceV4: ipNet("10.0.0.1/32"),
		SourceV6: ipNet("2001:db8:a::/96"),
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

func TestUpdateConfig(t *testing.T) {
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

func TestUpdateConfigOneSource(t *testing.T) {
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

func TestUpdateConfigNoServices(t *testing.T) {
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

func TestUpdateConfigBothTransportsOnOnePort(t *testing.T) {
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

func TestUpdateConfigRejects(t *testing.T) {
	tests := []struct {
		name    string
		request *unrduppb.UpdateConfigRequest
	}{
		{
			name:    "no name",
			request: &unrduppb.UpdateConfigRequest{Config: validConfig()},
		},
		{
			name:    "no config",
			request: &unrduppb.UpdateConfigRequest{Name: "unrdup0"},
		},
		{
			name: "no peers",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services[0].Peers = nil
			}),
		},
		{
			name: "no endpoints",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services[0].Endpoints = nil
			}),
		},
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
			name: "endpoint listed twice in one service",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services[0].Endpoints = append(
					config.Services[0].Endpoints,
					&unrduppb.Endpoint{
						Port:     443,
						Protocol: unrduppb.Protocol_PROTOCOL_TCP,
					},
				)
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
			name: "port zero",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services[0].Endpoints[0].Port = 0
			}),
		},
		{
			name: "port beyond sixteen bits",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services[0].Endpoints[0].Port = 65536
			}),
		},
		{
			name: "protocol left unspecified",
			request: withConfig(func(config *unrduppb.Config) {
				config.Services[0].Endpoints[0].Protocol =
					unrduppb.Protocol_PROTOCOL_UNSPECIFIED
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
				config.SourceV4 = ipNet("0.0.0.0/32")
			}),
		},
		{
			name: "source of the wrong family for its field",
			request: withConfig(func(config *unrduppb.Config) {
				config.SourceV4 = ipNet("2001:db8:a::/96")
			}),
		},
		{
			name: "source mask scattered",
			request: withConfig(func(config *unrduppb.Config) {
				config.SourceV4 = &filterpb.IPNet{
					Addr: []byte{10, 0, 0, 1},
					Mask: []byte{255, 0, 255, 0},
				}
			}),
		},
		{
			name: "source mask frees the whole address",
			request: withConfig(func(config *unrduppb.Config) {
				config.SourceV4 = &filterpb.IPNet{
					Addr: []byte{10, 0, 0, 1},
					Mask: []byte{0, 0, 0, 0},
				}
			}),
		},
		{
			name: "source mask of the wrong length",
			request: withConfig(func(config *unrduppb.Config) {
				config.SourceV4 = &filterpb.IPNet{
					Addr: []byte{10, 0, 0, 1},
					Mask: []byte{255, 255},
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

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeBackend{}
			service := unrdup.NewUnrdupService(backend)

			_, err := service.UpdateConfig(t.Context(), test.request)

			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Zero(t, backend.calls, "nothing may reach shared memory")
		})
	}
}

func withConfig(mutate func(config *unrduppb.Config)) *unrduppb.UpdateConfigRequest {
	config := validConfig()
	mutate(config)

	return &unrduppb.UpdateConfigRequest{Name: "unrdup0", Config: config}
}

func TestUpdateConfigReplaces(t *testing.T) {
	backend := &fakeBackend{}
	service := unrdup.NewUnrdupService(backend)

	request := &unrduppb.UpdateConfigRequest{Name: "unrdup0", Config: validConfig()}

	_, err := service.UpdateConfig(t.Context(), request)
	require.NoError(t, err)

	_, err = service.UpdateConfig(t.Context(), request)
	require.NoError(t, err)

	require.Len(t, backend.handles, 2)
	require.True(t, backend.handles[0].freed, "the replaced handle is released")
	require.False(t, backend.handles[1].freed, "the live handle is kept")
}

func TestUpdateConfigBackendFailure(t *testing.T) {
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

func TestUpdateConfigRejectsUnusableNames(t *testing.T) {
	for name, configName := range map[string]string{
		"embedded NUL": "unrdup\x000",
		"too long":     strings.Repeat("u", 80),
	} {
		t.Run(name, func(t *testing.T) {
			backend := &fakeBackend{}
			service := unrdup.NewUnrdupService(backend)

			_, err := service.UpdateConfig(t.Context(), &unrduppb.UpdateConfigRequest{
				Name:   configName,
				Config: validConfig(),
			})
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Zero(t, backend.calls)
		})
	}
}

func TestUpdateConfigReplacementFailureKeepsPrevious(t *testing.T) {
	backend := &fakeBackend{}
	service := unrdup.NewUnrdupService(backend)

	_, err := service.UpdateConfig(t.Context(), &unrduppb.UpdateConfigRequest{
		Name:   "unrdup0",
		Config: validConfig(),
	})
	require.NoError(t, err)
	require.Len(t, backend.handles, 1)

	backend.err = errBackendFailure

	replacement := validConfig()
	replacement.Services[0].Vip = ipAddr("192.0.2.2")

	_, err = service.UpdateConfig(t.Context(), &unrduppb.UpdateConfigRequest{
		Name:   "unrdup0",
		Config: replacement,
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
	require.False(t, backend.handles[0].freed)

	response, err := service.ShowConfig(t.Context(), &unrduppb.ShowConfigRequest{
		Name: "unrdup0",
	})
	require.NoError(t, err)

	stored := response.GetConfig().GetServices()
	require.Len(t, stored, 1)
	require.Equal(t, ipAddr("192.0.2.1").GetAddr(), stored[0].GetVip().GetAddr())
}

func TestShowConfig(t *testing.T) {
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
	require.Equal(t, []byte{10, 0, 0, 1}, config.GetSourceV4().GetAddr())
	require.Equal(t, net.CIDRMask(32, 32), net.IPMask(config.GetSourceV4().GetMask()))
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

func TestShowConfigNotFound(t *testing.T) {
	service := unrdup.NewUnrdupService(&fakeBackend{})

	_, err := service.ShowConfig(t.Context(), &unrduppb.ShowConfigRequest{
		Name: "missing",
	})

	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestShowConfigRequiresName(t *testing.T) {
	service := unrdup.NewUnrdupService(&fakeBackend{})

	_, err := service.ShowConfig(t.Context(), &unrduppb.ShowConfigRequest{})

	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListConfigs(t *testing.T) {
	service := unrdup.NewUnrdupService(&fakeBackend{})

	for _, name := range []string{"unrdup1", "unrdup0"} {
		_, err := service.UpdateConfig(t.Context(), &unrduppb.UpdateConfigRequest{
			Name:   name,
			Config: validConfig(),
		})
		require.NoError(t, err)
	}

	response, err := service.ListConfigs(t.Context(), &unrduppb.ListConfigsRequest{})
	require.NoError(t, err)

	require.Equal(t, []string{"unrdup0", "unrdup1"}, response.GetConfigs())
}

func TestListConfigsEmpty(t *testing.T) {
	service := unrdup.NewUnrdupService(&fakeBackend{})

	response, err := service.ListConfigs(t.Context(), &unrduppb.ListConfigsRequest{})
	require.NoError(t, err)

	require.Empty(t, response.GetConfigs())
}
