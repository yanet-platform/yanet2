package l3b_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
	"github.com/yanet-platform/yanet2/modules/l3b/internal/testutils"
	"github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
)

// Test_Backend_CreateService_RequiredCounterFailurePublishesNothing verifies
// that repeated refusals expose no partial service or table and reclaim memory.
func Test_Backend_CreateService_RequiredCounterFailurePublishesNothing(t *testing.T) {
	for _, tc := range testutils.RequiredCounterFailures() {
		t.Run(tc.Name, func(t *testing.T) {
			backend, agent, harness := newTestBackendFixture(t)
			service := sampleService("svc")
			service.SessionIndexSize = 64
			service.RealServers = []*l3bpb.RealServer{
				{DestinationAddress: []byte{172, 16, 0, 10}, SourceNetwork: commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("192.0.2.0/24"))},
				{DestinationAddress: []byte{172, 16, 0, 11}, SourceNetwork: commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("192.0.2.0/24"))},
			}
			capacity := agent.BlockAllocatorFreeSize()
			generation, err := harness.PublishedExecutionContext(0)
			require.NoError(t, err)
			require.Zero(t, generation)
			for range 3 {
				witness, err := testutils.WithCounterFailure("svc", tc, func() error {
					return backend.CreateService(service)
				})
				require.Error(t, err)
				require.Equal(t, testutils.CounterWitness{Matches: 1, Failures: 1, RegisteredBefore: tc.RegisteredBefore}, witness)
				require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
				require.Empty(t, backend.ListServices())
				view, err := backend.GetService("svc")
				require.Nil(t, view)
				require.Equal(t, codes.NotFound, status.Code(err))
				current, err := harness.PublishedExecutionContext(0)
				require.NoError(t, err)
				require.Equal(t, generation, current)
			}
			require.NoError(t, backend.CreateService(service))
			require.Equal(t, []string{"svc"}, backend.ListServices())
			view, err := backend.GetService("svc")
			require.NoError(t, err)
			require.Len(t, view.GetRealServers(), 2)
			for _, real := range view.GetRealServers() {
				require.False(t, real.Enabled)
				require.Equal(t, uint32(1), real.Weight)
			}
			ring, err := testutils.ReadRealRing(agent, "svc", 2)
			require.NoError(t, err)
			require.Empty(t, ring)
			current, err := harness.PublishedExecutionContext(0)
			require.NoError(t, err)
			require.NotZero(t, current)
			packets, bytes, err := cl3bobject.ReadServiceCounter(current, "svc", "incoming")
			require.NoError(t, err)
			require.Zero(t, packets)
			require.Zero(t, bytes)
			require.NoError(t, backend.DeleteService("svc"))
		})
	}
}
