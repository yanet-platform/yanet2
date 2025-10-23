package test_balancer

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	balancer "github.com/yanet-platform/yanet2/modules/balancer1/controlplane"
)

////////////////////////////////////////////////////////////////////////////////

func TestCreateBalancer(t *testing.T) {
	mock, err := NewMock(1 << 21)
	require.Nil(t, err, "failed to create mock: %s", err)
	defer FreeMock(&mock)
	agent, err := mock.CreateAgent(1 << 20)
	require.Nil(t, err, "failed to create agent: %s", err)
	config := balancer.BalancerConfig{
		Services: []balancer.VirtualService{
			{
				Address: IpAddr("192.166.13.22"),
				Port:    1000,
				Flags: balancer.VsFlags{
					GRE:    false,
					OPS:    false,
					PureL3: false,
					FixMSS: false,
				},
				Proto: balancer.VsProtoTcp,
				AllowedSrc: []netip.Prefix{
					IpPrefix("10.12.0.0/8"),
				},
				Reals: []balancer.Real{
					{
						Weight:  1,
						DstAddr: IpAddr("1.1.1.1"),
						SrcAddr: IpAddr("3.3.3.3"),
						SrcMask: IpAddr("255.240.255.0"),
						Enabled: true,
					},
				},
			},
		},
	}
	b, err := balancer.NewBalancerInstance(&agent, &config, 100)
	require.Nil(t, err, "failed to create new balancer instance")
	b.Free()
}
