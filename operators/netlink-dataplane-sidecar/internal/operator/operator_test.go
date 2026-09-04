package operator_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"google.golang.org/grpc"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
)

type fakeNetlinkHandle struct {
	closed bool
}

func (m *fakeNetlinkHandle) LinkList() ([]vnetlink.Link, error) {
	return nil, nil
}

func (m *fakeNetlinkHandle) LinkByName(string) (vnetlink.Link, error) {
	return nil, nil
}

func (m *fakeNetlinkHandle) LinkByIndex(int) (vnetlink.Link, error) {
	return nil, nil
}

func (m *fakeNetlinkHandle) LinkAdd(vnetlink.Link) error {
	return nil
}

func (m *fakeNetlinkHandle) LinkDel(vnetlink.Link) error {
	return nil
}

func (m *fakeNetlinkHandle) LinkSetAlias(vnetlink.Link, string) error {
	return nil
}

func (m *fakeNetlinkHandle) LinkSetMTU(vnetlink.Link, int) error {
	return nil
}

func (m *fakeNetlinkHandle) LinkSetUp(vnetlink.Link) error {
	return nil
}

func (m *fakeNetlinkHandle) AddrList(vnetlink.Link, int) ([]vnetlink.Addr, error) {
	return nil, nil
}

func (m *fakeNetlinkHandle) AddrReplace(vnetlink.Link, *vnetlink.Addr) error {
	return nil
}

func (m *fakeNetlinkHandle) AddrDel(vnetlink.Link, *vnetlink.Addr) error {
	return nil
}

func (m *fakeNetlinkHandle) RouteListFiltered(
	int,
	*vnetlink.Route,
	uint64,
) ([]vnetlink.Route, error) {
	return nil, nil
}

func (m *fakeNetlinkHandle) RouteReplace(*vnetlink.Route) error {
	return nil
}

func (m *fakeNetlinkHandle) RouteAdd(*vnetlink.Route) error {
	return nil
}

func (m *fakeNetlinkHandle) RouteDel(*vnetlink.Route) error {
	return nil
}

func (m *fakeNetlinkHandle) NeighList(int, int) ([]vnetlink.Neigh, error) {
	return nil, nil
}

func (m *fakeNetlinkHandle) Close() {
	m.closed = true
}

type fakeGatewayConnection struct {
	closed bool
}

func (m *fakeGatewayConnection) Invoke(
	context.Context,
	string,
	any,
	any,
	...grpc.CallOption,
) error {
	return nil
}

func (m *fakeGatewayConnection) NewStream(
	context.Context,
	*grpc.StreamDesc,
	string,
	...grpc.CallOption,
) (grpc.ClientStream, error) {
	return nil, nil
}

func (m *fakeGatewayConnection) Close() error {
	m.closed = true
	return nil
}

func TestNewOperatorCleansResourcesAfterPartialDialFailure(t *testing.T) {
	handle := &fakeNetlinkHandle{}
	firstConnection := &fakeGatewayConnection{}
	dialErr := errors.New("second dial failed")
	dialCount := 0

	runnable, err := sidecaroperator.NewOperator(
		twoGatewayConfig(),
		sidecaroperator.WithNetlinkHandleFactory(func() (
			sidecaroperator.NetlinkHandle,
			error,
		) {
			return handle, nil
		}),
		sidecaroperator.WithGatewayDialer(func(
			commonoperator.GatewayConfig,
		) (sidecaroperator.GatewayConnection, error) {
			dialCount++
			if dialCount == 1 {
				return firstConnection, nil
			}
			return nil, dialErr
		}),
	)

	require.Nil(t, runnable)
	require.ErrorIs(t, err, dialErr)
	require.True(t, firstConnection.closed)
	require.True(t, handle.closed)
}

func TestNewOperatorClosesHandleReturnedWithFactoryError(t *testing.T) {
	handle := &fakeNetlinkHandle{}
	factoryErr := errors.New("partial handle failure")

	runnable, err := sidecaroperator.NewOperator(
		twoGatewayConfig(),
		sidecaroperator.WithNetlinkHandleFactory(func() (
			sidecaroperator.NetlinkHandle,
			error,
		) {
			return handle, factoryErr
		}),
	)

	require.Nil(t, runnable)
	require.ErrorIs(t, err, factoryErr)
	require.True(t, handle.closed)
}

func TestNewOperatorRejectsInvalidEndpointsBeforeCreatingNetlinkHandle(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*sidecaroperator.Config)
	}{
		{
			name: "malformed server endpoint",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Server.Endpoint = xcfg.MustNonEmptyString("::1:8080")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := twoGatewayConfig()
			test.mutate(cfg)
			handleCreated := false

			runnable, err := sidecaroperator.NewOperator(
				cfg,
				sidecaroperator.WithNetlinkHandleFactory(func() (
					sidecaroperator.NetlinkHandle,
					error,
				) {
					handleCreated = true
					return &fakeNetlinkHandle{}, nil
				}),
			)

			require.Error(t, err)
			require.Nil(t, runnable)
			require.False(t, handleCreated)
		})
	}
}

func TestOperatorCloseReleasesEveryConnectionAndHandle(t *testing.T) {
	handle := &fakeNetlinkHandle{}
	connections := []*fakeGatewayConnection{{}, {}}
	dialCount := 0
	runnable, err := sidecaroperator.NewOperator(
		twoGatewayConfig(),
		sidecaroperator.WithNetlinkHandleFactory(func() (
			sidecaroperator.NetlinkHandle,
			error,
		) {
			return handle, nil
		}),
		sidecaroperator.WithGatewayDialer(func(
			commonoperator.GatewayConfig,
		) (sidecaroperator.GatewayConnection, error) {
			connection := connections[dialCount]
			dialCount++
			return connection, nil
		}),
	)
	require.NoError(t, err)

	require.NoError(t, runnable.Close())
	require.True(t, connections[0].closed)
	require.True(t, connections[1].closed)
	require.True(t, handle.closed)
}

func twoGatewayConfig() *sidecaroperator.Config {
	cfg := sidecaroperator.DefaultConfig()
	cfg.Gateways = []sidecaroperator.GatewayConfig{
		{
			Name:              "numa0",
			Endpoint:          xcfg.MustNonEmptyString(net.JoinHostPort("::1", "8080")),
			NeighbourTable:    "netlink-dataplane-numa0",
			NeighbourPriority: sidecaroperator.DefaultNeighbourPriority,
			Devices:           []string{"logical0"},
		},
		{
			Name:              "numa1",
			Endpoint:          xcfg.MustNonEmptyString(net.JoinHostPort("::1", "8081")),
			NeighbourTable:    "netlink-dataplane-numa1",
			NeighbourPriority: sidecaroperator.DefaultNeighbourPriority,
			Devices:           []string{"logical1"},
		},
	}
	return cfg
}
