package gateway_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/gateway"
)

// stubAddr is a net.Addr with a fixed string form, standing in for a listener.
type stubAddr struct {
	value string
}

func (m *stubAddr) Network() string {
	return "tcp"
}

func (m *stubAddr) String() string {
	return m.value
}

func Test_AdvertisedEndpoint_UnsetFallsBackToListenerAddress(t *testing.T) {
	// verifies that an empty advertised address keeps the previous behaviour,
	// leaving an existing config unaffected.
	require.Equal(t,
		"[::1]:8080",
		gateway.AdvertisedEndpoint("", &stubAddr{value: "[::1]:8080"}),
	)
}

func Test_AdvertisedEndpoint_SetOverridesListenerAddress(t *testing.T) {
	// verifies that a configured address wins over the resolved one, letting a
	// component advertise an address it cannot bind.
	require.Equal(t,
		"route-operator.yanet:8080",
		gateway.AdvertisedEndpoint("route-operator.yanet:8080", &stubAddr{value: "[::]:8080"}),
	)
}

func Test_AdvertisedEndpoint_SetKeepsNameUnresolved(t *testing.T) {
	// verifies that a name is passed through verbatim, since the gateway is the
	// side that resolves it on each dial.
	const advertised = "route-operator.yanet.svc.cluster.local:8080"

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, listener.Close())
	}()

	got := gateway.AdvertisedEndpoint(advertised, listener.Addr())

	require.Equal(t, advertised, got)
	require.NotEqual(t, listener.Addr().String(), got)
}

func Test_AdvertisedEndpoint_UnsetOnWildcardBindYieldsUnreachableAddress(t *testing.T) {
	// verifies the failure this field exists to avoid: unset, a wildcard bind
	// registers an address that sends the gateway to its own loopback.
	listener, err := net.Listen("tcp", "[::]:0")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, listener.Close())
	}()

	host, _, err := net.SplitHostPort(gateway.AdvertisedEndpoint("", listener.Addr()))
	require.NoError(t, err)
	require.Equal(t, "::", host)
}
