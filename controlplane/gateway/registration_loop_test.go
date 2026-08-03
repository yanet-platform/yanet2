package gateway_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// reserveTCPAddr reserves and immediately releases an ephemeral TCP port so a
// server that binds it later gets a concrete, otherwise-unused endpoint.
func reserveTCPAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	return addr
}

// findRegisteredService looks up a service by name in a ListServices
// response, returning the entry and whether it was found.
func findRegisteredService(response *ynpb.ListServicesResponse, name string) (*ynpb.RegisteredBackend, bool) {
	for _, entry := range response.GetServices() {
		if entry.GetBackend().GetName() == name {
			return entry, true
		}
	}
	return nil, false
}

// TestGateway_RegistrationLoop_SurvivesSweepsThatEvictOneShot verifies that a
// service kept alive by RegistrationLoop survives many registry sweeps that
// evict a sibling registered only once.
//
// It reproduces a production outage where an operator that registered once
// vanished from the gateway as soon as the sweeper's TTL elapsed. Both the
// looped service and the one-shot control run against the same gateway, so
// the contrast shares one sweeper and one clock.
func TestGateway_RegistrationLoop_SurvivesSweepsThatEvictOneShot(t *testing.T) {
	t.Parallel()

	cfg := gateway.DefaultConfig()
	cfg.Server.Endpoint = reserveTCPAddr(t)
	cfg.Registry.TTL = xcfg.MustNonZero(time.Second)
	cfg.Registry.SweepInterval = xcfg.MustNonZero(25 * time.Millisecond)

	gw, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())

	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		return gw.Run(groupContext)
	})

	connection, err := grpc.NewClient(cfg.Server.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	client := ynpb.NewGatewayClient(connection)

	require.Eventually(t, func() bool {
		_, listErr := client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		return listErr == nil
	}, 5*time.Second, 50*time.Millisecond, "failed to reach the gateway's ListServices RPC")

	loopedServiceName := "test.LoopedService"
	oneShotServiceName := "test.OneShotService"
	loopedBackendAddr := reserveTCPAddr(t)
	oneShotBackendAddr := reserveTCPAddr(t)

	constantBackOff := func() backoff.BackOff {
		return backoff.NewConstantBackOff(10 * time.Millisecond)
	}

	oneShotRegistrar, err := gateway.NewGatewayRegistrar(cfg.Server.Endpoint, nil,
		gateway.WithBackOff(constantBackOff),
		gateway.WithMaxElapsedTime(time.Second),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = oneShotRegistrar.Close() })

	require.NoError(t, oneShotRegistrar.RegisterServices(t.Context(), []string{oneShotServiceName}, oneShotBackendAddr))

	loopedRegistrar, err := gateway.NewGatewayRegistrar(cfg.Server.Endpoint, nil,
		gateway.WithBackOff(constantBackOff),
		gateway.WithMaxElapsedTime(time.Second),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = loopedRegistrar.Close() })

	registrationLoop := gateway.NewRegistrationLoop(
		loopedRegistrar,
		[]string{loopedServiceName},
		loopedBackendAddr,
		gateway.WithLoopInterval(20*time.Millisecond),
	)
	group.Go(func() error {
		return registrationLoop.Run(groupContext)
	})

	// Vacuity guard: both services must actually reach the registry as
	// sweeper-eligible external backends, otherwise the eviction and
	// survival assertions below would trivially hold for entries the
	// sweeper never considers.
	var firstLoopedLastSeenAt time.Time
	require.Eventually(t, func() bool {
		response, listErr := client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		if listErr != nil {
			return false
		}

		loopedEntry, loopedFound := findRegisteredService(response, loopedServiceName)
		oneShotEntry, oneShotFound := findRegisteredService(response, oneShotServiceName)
		if !loopedFound || !oneShotFound {
			return false
		}
		if loopedEntry.GetKind() != ynpb.BackendKind_BACKEND_KIND_EXTERNAL {
			return false
		}
		if oneShotEntry.GetKind() != ynpb.BackendKind_BACKEND_KIND_EXTERNAL {
			return false
		}

		firstLoopedLastSeenAt = loopedEntry.GetLastSeenAt().AsTime()
		return true
	}, 5*time.Second, 20*time.Millisecond, "both services must register as external backends before the eviction race begins")

	// Negative control: the one-shot registration is never refreshed, so
	// the sweeper must evict it once the TTL elapses. This also proves the
	// sweeper is alive and that at least one TTL window has passed.
	require.Eventually(t, func() bool {
		response, listErr := client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		if listErr != nil {
			return false
		}
		_, found := findRegisteredService(response, oneShotServiceName)
		return !found
	}, 5*time.Second, 10*time.Millisecond, "sweeper must evict a one-shot registration once its TTL elapses")

	// Positive case: from the moment the one-shot control is gone, the
	// looped service must survive many further sweep ticks, because its
	// registration loop keeps refreshing the last-seen timestamp.
	require.Never(t, func() bool {
		response, listErr := client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
		if listErr != nil {
			assert.NoError(t, listErr)
			return true
		}
		_, found := findRegisteredService(response, loopedServiceName)
		return !found
	}, 1500*time.Millisecond, 5*time.Millisecond, "a service kept alive by RegistrationLoop must not be evicted by the sweeper")

	finalResponse, err := client.ListServices(t.Context(), &ynpb.ListServicesRequest{})
	require.NoError(t, err)

	// Refresh, not a frozen entry: presence alone would also hold for an
	// entry the sweeper simply never examined, so the timestamp itself
	// must have advanced past the first observation.
	loopedEntry, found := findRegisteredService(finalResponse, loopedServiceName)
	require.True(t, found, "looped service must still be present at the end of the test")
	require.True(t, loopedEntry.GetLastSeenAt().AsTime().After(firstLoopedLastSeenAt),
		"looped service's last-seen timestamp must have advanced across sweeps, not stayed frozen")

	// The control stays gone, so the earlier eviction assertion cannot be
	// satisfied by a transient blip that later re-appears.
	_, oneShotStillFound := findRegisteredService(finalResponse, oneShotServiceName)
	require.False(t, oneShotStillFound, "one-shot registration must remain evicted")

	cancel()
	require.ErrorIs(t, group.Wait(), context.Canceled)
}
