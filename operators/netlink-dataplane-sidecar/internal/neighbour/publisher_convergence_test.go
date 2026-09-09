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

// unavailableClient fails one transport without accessing the receiver table.
type unavailableClient struct{ Calls int }

func (m *unavailableClient) ReplaceNeighbours(ctx context.Context, options ...grpc.CallOption) (grpc.ClientStreamingClient[operatorpb.ReplaceNeighboursRequest, operatorpb.ReplaceNeighboursResponse], error) {
	m.Calls++
	return nil, status.Error(codes.Unavailable, "transport unavailable")
}

// Test_Publish_FallbackAndLostResponse verifies that an unknown commit outcome
// can retry the same complete snapshot and stops after the first acknowledged success.
func Test_Publish_FallbackAndLostResponse(t *testing.T) {
	service, client := newPublicationService(t)
	failed := &unavailableClient{}
	unused := &unavailableClient{}
	config := publicationConfig()
	entries := []neighbour.Entry{testDesiredEntry("fe80::1", "logical0"), testDesiredEntry("fe80::1", "logical1")}
	service.SetHook(func(call publicationCall) error {
		if call.Method == "response" {
			service.SetHook(nil)
			return status.Error(codes.Unavailable, "response lost after commit")
		}
		return nil
	})
	targets := []neighbour.GatewayTarget{newPublisherTarget("unavailable", failed), newPublisherTarget("lost-response", client), newPublisherTarget("retry", client), newPublisherTarget("unused", unused)}
	require.NoError(t, neighbour.Publish(t.Context(), entries, targets, config))
	require.Equal(t, 1, failed.Calls)
	require.Zero(t, unused.Calls)
	calls := service.Calls()
	require.Len(t, calls, 6)
	require.True(t, proto.Equal(calls[0].Chunk, calls[3].Chunk))
	require.Len(t, service.Tables()[config.TableName].Entries, 2)
}

// Test_Publish_FailureKeepsSnapshot verifies that interrupted and refused streams
// preserve last-good data and a later complete replacement recovers the table.
func Test_Publish_FailureKeepsSnapshot(t *testing.T) {
	for _, method := range []string{"chunk", "commit"} {
		t.Run(method, func(t *testing.T) {
			service, client := newPublicationService(t)
			config := publicationConfig()
			service.Store(config.TableName, publicationTable{Priority: 7})
			service.SetHook(func(call publicationCall) error {
				if call.Method == method {
					return status.Error(codes.Unavailable, "interrupted")
				}
				return nil
			})
			targets := []neighbour.GatewayTarget{newPublisherTarget("first", client), newPublisherTarget("second", client)}
			require.Error(t, neighbour.Publish(t.Context(), nil, targets, config))
			require.Equal(t, uint32(7), service.Tables()[config.TableName].Priority)
			service.SetHook(nil)
			require.NoError(t, neighbour.Publish(t.Context(), nil, targets, config))
			require.Equal(t, config.DefaultPriority, service.Tables()[config.TableName].Priority)
		})
	}
}

// Test_Publish_DeadlineFallback verifies that each attempt has its own deadline
// and expiration leaves enough parent budget to retry the same table.
func Test_Publish_DeadlineFallback(t *testing.T) {
	service, client := newPublicationService(t)
	service.SetHook(func(call publicationCall) error {
		service.SetHook(nil)
		<-call.Context.Done()
		return status.FromContextError(call.Context.Err()).Err()
	})
	config := publicationConfig()
	config.Timeout = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, neighbour.Publish(ctx, nil, []neighbour.GatewayTarget{newPublisherTarget("timeout", client), newPublisherTarget("retry", client)}, config))
	require.NoError(t, ctx.Err())
	require.Contains(t, service.Tables(), config.TableName)
}

// Test_Publish_ConfiguredDeadline verifies that a larger configured timeout
// reaches the server instead of being capped by a hard-coded default.
func Test_Publish_ConfiguredDeadline(t *testing.T) {
	service, client := newPublicationService(t)
	remaining := make(chan time.Duration, 3)
	service.SetHook(func(call publicationCall) error {
		deadline, _ := call.Context.Deadline()
		remaining <- time.Until(deadline)
		return nil
	})
	config := publicationConfig()
	config.Timeout = 15 * time.Second
	require.NoError(t, neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{newPublisherTarget("first", client)}, config))
	require.Greater(t, <-remaining, 10*time.Second)
}

// Test_Publish_Cancellation verifies that cancellation prevents further attempts
// and cannot turn an incomplete stream into a committed empty snapshot.
func Test_Publish_Cancellation(t *testing.T) {
	for _, method := range []string{"before", "chunk", "commit"} {
		t.Run(method, func(t *testing.T) {
			service, client := newPublicationService(t)
			config := publicationConfig()
			service.Store(config.TableName, publicationTable{Priority: 7})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			service.SetHook(func(call publicationCall) error {
				if call.Method == method {
					cancel()
					<-call.Context.Done()
				}
				return nil
			})
			if method == "before" {
				cancel()
			}
			unused := &unavailableClient{}
			require.Error(t, neighbour.Publish(ctx, nil, []neighbour.GatewayTarget{newPublisherTarget("first", client), newPublisherTarget("unused", unused)}, config))
			require.Zero(t, unused.Calls)
			require.Equal(t, uint32(7), service.Tables()[config.TableName].Priority)
		})
	}
}
