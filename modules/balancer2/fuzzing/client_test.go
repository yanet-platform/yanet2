package fuzzing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// fakeCall captures a single RPC invocation for ordering, deadline, and
// payload assertions. Tests inspect the slice returned by fakeBalancerClient.
type fakeCall struct {
	op       string
	hasDl    bool
	deadline time.Time
	request  any
}

// fakeBalancerClient is the in-test BalancerClient used to drive the RPC
// client without standing up a controlplane. The errOn map injects an
// error for a specific op on the next call to that op; calls is the
// recorded history.
type fakeBalancerClient struct {
	balancerpb.BalancerClient

	calls []fakeCall
	errOn map[string]error
}

func newFakeBalancerClient() *fakeBalancerClient {
	return &fakeBalancerClient{errOn: map[string]error{}}
}

// record appends a call entry and consumes any matching injected error.
func (m *fakeBalancerClient) record(ctx context.Context, op string, req any) error {
	deadline, ok := ctx.Deadline()
	m.calls = append(m.calls, fakeCall{
		op:       op,
		hasDl:    ok,
		deadline: deadline,
		request:  req,
	})
	if err, has := m.errOn[op]; has {
		delete(m.errOn, op)
		return err
	}
	return nil
}

func (m *fakeBalancerClient) UpdateSessionsState(
	_ context.Context,
	_ *balancerpb.UpdateSessionsStateRequest,
	_ ...grpc.CallOption,
) (*balancerpb.UpdateSessionsStateResponse, error) {
	return &balancerpb.UpdateSessionsStateResponse{}, nil
}

func (m *fakeBalancerClient) UpdateConfig(
	ctx context.Context,
	in *balancerpb.UpdateConfigRequest,
	_ ...grpc.CallOption,
) (*balancerpb.UpdateConfigResponse, error) {
	if err := m.record(ctx, RPCUpdateConfig, in); err != nil {
		return nil, err
	}
	return &balancerpb.UpdateConfigResponse{}, nil
}

func (m *fakeBalancerClient) UpdateVS(
	ctx context.Context,
	in *balancerpb.UpdateVSRequest,
	_ ...grpc.CallOption,
) (*balancerpb.UpdateVSResponse, error) {
	if err := m.record(ctx, RPCUpdateVS, in); err != nil {
		return nil, err
	}
	return &balancerpb.UpdateVSResponse{}, nil
}

func (m *fakeBalancerClient) DeleteVS(
	ctx context.Context,
	in *balancerpb.DeleteVSRequest,
	_ ...grpc.CallOption,
) (*balancerpb.DeleteVSResponse, error) {
	if err := m.record(ctx, RPCDeleteVS, in); err != nil {
		return nil, err
	}
	return &balancerpb.DeleteVSResponse{}, nil
}

func (m *fakeBalancerClient) UpdateReals(
	ctx context.Context,
	in *balancerpb.UpdateRealsRequest,
	_ ...grpc.CallOption,
) (*balancerpb.UpdateRealsResponse, error) {
	if err := m.record(ctx, RPCUpdateReals, in); err != nil {
		return nil, err
	}
	return &balancerpb.UpdateRealsResponse{}, nil
}

func (m *fakeBalancerClient) GetState(
	ctx context.Context,
	in *balancerpb.GetStateRequest,
	_ ...grpc.CallOption,
) (*balancerpb.GetStateResponse, error) {
	if err := m.record(ctx, RPCGetState, in); err != nil {
		return nil, err
	}
	return &balancerpb.GetStateResponse{}, nil
}

// newTestRPCClient builds an RPCClient backed by the supplied fake
// client and a fresh LatencyStats. requestTimeout is fixed at 250ms so
// deadline assertions are easy to reason about.
func newTestRPCClient(t *testing.T, fake balancerpb.BalancerClient) (*RPCClient, *LatencyStats) {
	t.Helper()
	stats := NewLatencyStats()
	client, err := NewRPCClient(fake, 250*time.Millisecond, stats)
	require.NoError(t, err)
	return client, stats
}

// TestRPCClientLifecycle exercises every wrapper method to confirm it
// dispatches to the fake, applies a deadline derived from the configured
// timeout, and records exactly one latency sample under the matching op
// name. The set is intentionally small but covers the whole interface.
func TestRPCClientLifecycle(t *testing.T) {
	fake := newFakeBalancerClient()
	client, stats := newTestRPCClient(t, fake)

	ctx := context.Background()
	before := time.Now()

	_, err := client.UpdateVS(ctx, &balancerpb.UpdateVSRequest{ConfigName: "cfg"})
	require.NoError(t, err)

	_, err = client.DeleteVS(ctx, &balancerpb.DeleteVSRequest{ConfigName: "cfg"})
	require.NoError(t, err)

	_, err = client.UpdateReals(ctx, &balancerpb.UpdateRealsRequest{ConfigName: "cfg"})
	require.NoError(t, err)

	_, err = client.GetState(ctx, &balancerpb.GetStateRequest{ConfigName: "cfg"})
	require.NoError(t, err)

	// Each call must have produced exactly one fake invocation with a
	// deadline drawn from the per-RPC timeout. We allow some slack on
	// the upper bound to absorb scheduler jitter on busy CI machines.
	require.Len(t, fake.calls, 4)
	wantOps := []string{
		RPCUpdateVS,
		RPCDeleteVS,
		RPCUpdateReals,
		RPCGetState,
	}
	for idx, call := range fake.calls {
		assert.Equal(t, wantOps[idx], call.op, "op order at index %d", idx)
		require.True(t, call.hasDl, "call %s must carry a deadline", call.op)
		remaining := call.deadline.Sub(before)
		assert.LessOrEqual(t, remaining, 251*time.Millisecond,
			"deadline for %s must not exceed configured timeout (+1ms slack)", call.op)
		assert.Greater(t, remaining, 100*time.Millisecond,
			"deadline for %s must be reasonably close to the configured timeout", call.op)
	}

	// Each wrapper must record exactly one latency sample under the
	// matching op name. Report consumes the window so we can confirm
	// the cumulative counts in one shot.
	reports := stats.Report()
	require.Len(t, reports, len(wantOps))
	for _, op := range wantOps {
		var found bool
		for _, r := range reports {
			if r.Op == op {
				assert.Equal(t, uint64(1), r.Count, "exactly one sample for %s", op)
				assert.Equal(t, uint64(0), r.Errors, "no errors expected for %s", op)
				assert.Equal(t, uint64(1), r.CumulativeCount,
					"cumulative count must reflect the single call to %s", op)
				found = true
				break
			}
		}
		assert.True(t, found, "report missing entry for op %s", op)
	}
}

// TestRPCClientRecordsErrorSamples confirms that a failed RPC still
// appends a latency sample and bumps the error counter. The runner
// relies on this so percentile dashboards include failure latency.
func TestRPCClientRecordsErrorSamples(t *testing.T) {
	fake := newFakeBalancerClient()
	fake.errOn[RPCUpdateVS] = status.Error(codes.Unavailable, "transient")

	client, stats := newTestRPCClient(t, fake)
	_, err := client.UpdateVS(context.Background(), &balancerpb.UpdateVSRequest{ConfigName: "cfg"})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))

	reports := stats.Report()
	require.Len(t, reports, 1)
	assert.Equal(t, RPCUpdateVS, reports[0].Op)
	assert.Equal(t, uint64(1), reports[0].Count)
	assert.Equal(t, uint64(1), reports[0].Errors)
}

// TestRPCClientRequestTimeoutAppliesDeadline pins the relationship between
// RuntimeConfig.RequestTimeout and the deadline observed by the downstream
// balancer client.
func TestRPCClientRequestTimeoutAppliesDeadline(t *testing.T) {
	fake := newFakeBalancerClient()
	stats := NewLatencyStats()
	client, err := NewRPCClient(fake, 75*time.Millisecond, stats)
	require.NoError(t, err)

	before := time.Now()
	_, err = client.UpdateVS(context.Background(), &balancerpb.UpdateVSRequest{ConfigName: "cfg"})
	require.NoError(t, err)

	require.Len(t, fake.calls, 1)
	call := fake.calls[0]
	require.True(t, call.hasDl)
	remaining := call.deadline.Sub(before)
	assert.LessOrEqual(t, remaining, 76*time.Millisecond)
	assert.Greater(t, remaining, 30*time.Millisecond)
}

func TestNewRPCClientValidatesArguments(t *testing.T) {
	stats := NewLatencyStats()
	fake := newFakeBalancerClient()

	_, err := NewRPCClient(nil, time.Second, stats)
	require.Error(t, err)

	_, err = NewRPCClient(fake, time.Second, nil)
	require.Error(t, err)

	_, err = NewRPCClient(fake, 0, stats)
	require.Error(t, err)

	_, err = NewRPCClient(fake, -time.Millisecond, stats)
	require.Error(t, err)
}

// recordingRPC is a hand-rolled BalancerRPC fake used by tests that wire
// through RunMain or Runner with seams. It is deliberately distinct from
// fakeBalancerClient so those tests do not depend on RPCClient internals.
type recordingRPC struct {
	calls []string
}

func (m *recordingRPC) UpdateConfig(
	_ context.Context,
	_ *balancerpb.UpdateConfigRequest,
) (*balancerpb.UpdateConfigResponse, error) {
	m.calls = append(m.calls, RPCUpdateConfig)
	return &balancerpb.UpdateConfigResponse{}, nil
}

func (m *recordingRPC) UpdateVS(
	_ context.Context,
	_ *balancerpb.UpdateVSRequest,
) (*balancerpb.UpdateVSResponse, error) {
	m.calls = append(m.calls, RPCUpdateVS)
	return &balancerpb.UpdateVSResponse{}, nil
}

func (m *recordingRPC) DeleteVS(
	_ context.Context,
	_ *balancerpb.DeleteVSRequest,
) (*balancerpb.DeleteVSResponse, error) {
	m.calls = append(m.calls, RPCDeleteVS)
	return &balancerpb.DeleteVSResponse{}, nil
}

func (m *recordingRPC) UpdateReals(
	_ context.Context,
	_ *balancerpb.UpdateRealsRequest,
) (*balancerpb.UpdateRealsResponse, error) {
	m.calls = append(m.calls, RPCUpdateReals)
	return &balancerpb.UpdateRealsResponse{}, nil
}

func (m *recordingRPC) GetState(
	_ context.Context,
	_ *balancerpb.GetStateRequest,
) (*balancerpb.GetStateResponse, error) {
	m.calls = append(m.calls, RPCGetState)
	return &balancerpb.GetStateResponse{}, nil
}
