package neighbour_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// unavailableClient counts attempts without accessing receiver state.
type unavailableClient struct{ Calls int }

func (m *unavailableClient) ReplaceNeighbours(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest, options ...grpc.CallOption) (*operatorpb.ReplaceNeighboursResponse, error) {
	m.Calls++
	return nil, status.Error(codes.Unavailable, "transport unavailable")
}

// requestClient exposes the unary boundary without a second transport protocol.
type requestClient func(context.Context, *operatorpb.ReplaceNeighboursRequest) (*operatorpb.ReplaceNeighboursResponse, error)

func (m requestClient) ReplaceNeighbours(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest, options ...grpc.CallOption) (*operatorpb.ReplaceNeighboursResponse, error) {
	return m(ctx, request)
}

// Test_Publish_IndependentRequest verifies that fallback reuses the prepared
// snapshot despite later caller mutations and stops at the first acknowledgement.
func Test_Publish_IndependentRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "transport failure", err: status.Error(codes.Unavailable, "retry")},
		{name: "missing acknowledgement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []neighbour.Entry{testDesiredEntry("fe80::1", "logical0"), testDesiredEntry("::ffff:192.0.2.1", "logical1")}
			var requests []*operatorpb.ReplaceNeighboursRequest
			var expected *operatorpb.ReplaceNeighboursRequest
			failed := requestClient(func(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest) (*operatorpb.ReplaceNeighboursResponse, error) {
				requests = append(requests, request)
				expected = proto.Clone(request).(*operatorpb.ReplaceNeighboursRequest)
				entries[0] = testDesiredEntry("2001:db8::5", "changed")
				entries[1].HardwareRoute.DestinationMAC[5]++
				return nil, tc.err
			})
			retry := requestClient(func(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest) (*operatorpb.ReplaceNeighboursResponse, error) {
				requests = append(requests, request)
				return &operatorpb.ReplaceNeighboursResponse{}, nil
			})
			unused := &unavailableClient{}
			targets := []neighbour.GatewayTarget{newPublisherTarget("first", failed), newPublisherTarget("retry", retry), newPublisherTarget("unused", unused)}
			require.NoError(t, neighbour.Publish(t.Context(), entries, targets, publicationConfig()))
			require.Len(t, requests, 2)
			require.Same(t, requests[0], requests[1])
			require.True(t, proto.Equal(expected, requests[1]))
			require.Zero(t, unused.Calls)
		})
	}
}

// Test_Publish_NilResponse verifies that exhausting alternatives without an ACK
// returns an error rather than accepting an unknown outcome.
func Test_Publish_NilResponse(t *testing.T) {
	empty := requestClient(func(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest) (*operatorpb.ReplaceNeighboursResponse, error) {
		return nil, nil
	})
	failed := &unavailableClient{}
	targets := []neighbour.GatewayTarget{newPublisherTarget("unavailable", failed), newPublisherTarget("missing-response", empty)}
	require.ErrorContains(t, neighbour.Publish(t.Context(), nil, targets, publicationConfig()), "incomplete response")
	require.Equal(t, 1, failed.Calls)
}

// Test_Publish_DeadlineFallback verifies that expiration of one attempt leaves
// a fresh attempt and enough parent budget to retry the same request.
func Test_Publish_DeadlineFallback(t *testing.T) {
	var attempts []context.Context
	var first *operatorpb.ReplaceNeighboursRequest
	client := requestClient(func(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest) (*operatorpb.ReplaceNeighboursResponse, error) {
		attempts = append(attempts, ctx)
		if len(attempts) == 1 {
			first = request
			<-ctx.Done()
			return nil, ctx.Err()
		}
		require.NoError(t, ctx.Err())
		require.Same(t, first, request)
		return &operatorpb.ReplaceNeighboursResponse{}, nil
	})
	config := publicationConfig()
	config.Timeout = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	targets := []neighbour.GatewayTarget{newPublisherTarget("timeout", client), newPublisherTarget("retry", client)}
	require.NoError(t, neighbour.Publish(ctx, nil, targets, config))
	require.Len(t, attempts, 2)
	require.ErrorIs(t, attempts[0].Err(), context.DeadlineExceeded)
	require.NoError(t, ctx.Err())
}

// Test_Publish_ConfiguredDeadline verifies that a larger configured timeout
// reaches the client instead of being capped by a hard-coded default.
func Test_Publish_ConfiguredDeadline(t *testing.T) {
	var remaining time.Duration
	client := requestClient(func(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest) (*operatorpb.ReplaceNeighboursResponse, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		remaining = time.Until(deadline)
		return &operatorpb.ReplaceNeighboursResponse{}, nil
	})
	config := publicationConfig()
	config.Timeout = 15 * time.Second
	require.NoError(t, neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{newPublisherTarget("first", client)}, config))
	require.Greater(t, remaining, 10*time.Second)
}

// Test_Publish_Cancellation verifies that a cancelled parent prevents new
// attempts, whether cancelled before publishing or during a failed call.
func Test_Publish_Cancellation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		before    bool
		wantCalls int
	}{
		{name: "before first attempt", before: true},
		{name: "during failed attempt", wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			client := requestClient(func(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest) (*operatorpb.ReplaceNeighboursResponse, error) {
				calls++
				cancel()
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				return nil, ctx.Err()
			})
			if tc.before {
				cancel()
			}
			unused := &unavailableClient{}
			targets := []neighbour.GatewayTarget{newPublisherTarget("first", client), newPublisherTarget("unused", unused)}
			require.ErrorIs(t, neighbour.Publish(ctx, nil, targets, publicationConfig()), context.Canceled)
			require.Equal(t, tc.wantCalls, calls)
			require.Zero(t, unused.Calls)
		})
	}
}

// Test_Publish_AcknowledgementBeforeCancellation verifies that a successful ACK
// remains success even when the parent is cancelled before the call returns.
func Test_Publish_AcknowledgementBeforeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := requestClient(func(ctx context.Context, request *operatorpb.ReplaceNeighboursRequest) (*operatorpb.ReplaceNeighboursResponse, error) {
		cancel()
		return &operatorpb.ReplaceNeighboursResponse{}, nil
	})
	unused := &unavailableClient{}
	targets := []neighbour.GatewayTarget{newPublisherTarget("acknowledged", client), newPublisherTarget("unused", unused)}
	require.NoError(t, neighbour.Publish(ctx, nil, targets, publicationConfig()))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Zero(t, unused.Calls)
}
