package route_test

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
	sidecarpb "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/sidecarpb/v1"
)

// Test_Service_UpdateStaticRoutes_CommitsMultipleChunks verifies that a clean
// stream EOF installs all chunks as one snapshot and closes with a response.
func Test_Service_UpdateStaticRoutes_CommitsMultipleChunks(t *testing.T) {
	store, service := newTestService(t, 3, nil)
	stream := &fakeUpdateStream{ctx: t.Context(), requests: []*sidecarpb.UpdateStaticRoutesRequest{
		{Routes: []*sidecarpb.StaticRoute{
			mustProtoRoute(t, "192.0.2.0/24", "192.0.2.1", "kni0"),
		}},
		{Routes: []*sidecarpb.StaticRoute{
			mustProtoRoute(t, "192.0.2.0/24", "192.0.2.2", "kni1"),
			mustProtoRoute(t, "2001:db8::/64", "2001:db8::1", "kni2"),
		}},
	}}

	require.NoError(t, service.UpdateStaticRoutes(stream))
	require.NotNil(t, stream.response)
	snapshot, initialized := store.SnapshotState()
	require.True(t, initialized)
	require.Equal(t, []route.Route{
		testRoute("192.0.2.0/24", "192.0.2.1", "kni0"),
		testRoute("192.0.2.0/24", "192.0.2.2", "kni1"),
		testRoute("2001:db8::/64", "2001:db8::1", "kni2"),
	}, snapshot)
}

// Test_Service_UpdateStaticRoutes_WaitsForKernelApplication verifies that a
// staged snapshot wakes reconciliation but is acknowledged only after success.
func Test_Service_UpdateStaticRoutes_WaitsForKernelApplication(t *testing.T) {
	store := route.NewStore()
	service, err := route.NewService(store, route.ServiceConfig{MaxRoutes: 1})
	require.NoError(t, err)
	stream := &fakeUpdateStream{ctx: t.Context(), requests: []*sidecarpb.UpdateStaticRoutesRequest{{
		Routes: []*sidecarpb.StaticRoute{
			mustProtoRoute(t, "192.0.2.0/24", "192.0.2.1", "kni0"),
		},
	}}}
	result := make(chan error, 1)
	go func() {
		result <- service.UpdateStaticRoutes(stream)
	}()

	select {
	case <-store.Wake():
	case <-t.Context().Done():
		t.Fatal("route snapshot did not wake reconciliation")
	}
	select {
	case err := <-result:
		t.Fatalf("service returned before kernel application: %v", err)
	default:
	}
	_, _, update := store.SnapshotUpdate()
	update.Complete(nil)

	require.NoError(t, <-result)
	require.NotNil(t, stream.response)
}

// Test_Service_UpdateStaticRoutes_ReturnsKernelApplicationFailure verifies that
// failed application returns Unavailable and restores the prior route snapshot.
func Test_Service_UpdateStaticRoutes_ReturnsKernelApplicationFailure(t *testing.T) {
	store := route.NewStore()
	initial := []route.Route{testRoute("198.51.100.0/24", "198.51.100.1", "kni1")}
	require.NoError(t, store.Replace(initial))
	require.True(t, wakeReady(store.Wake()))
	service, err := route.NewService(store, route.ServiceConfig{MaxRoutes: 1})
	require.NoError(t, err)
	stream := &fakeUpdateStream{ctx: t.Context(), requests: []*sidecarpb.UpdateStaticRoutesRequest{{
		Routes: []*sidecarpb.StaticRoute{
			mustProtoRoute(t, "192.0.2.0/24", "192.0.2.1", "kni0"),
		},
	}}}
	result := make(chan error, 1)
	go func() {
		result <- service.UpdateStaticRoutes(stream)
	}()

	select {
	case <-store.Wake():
	case <-t.Context().Done():
		t.Fatal("route snapshot did not wake reconciliation")
	}
	_, _, update := store.SnapshotUpdate()
	update.Complete(errors.New("netlink apply failed"))

	err = <-result
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.ErrorContains(t, err, "netlink apply failed")
	require.Nil(t, stream.response)
	snapshot, initialized := store.SnapshotState()
	require.True(t, initialized)
	require.Equal(t, initial, snapshot)
	require.True(t, wakeReady(store.Wake()))
}

// Test_Service_UpdateStaticRoutes_ExplicitEmptyChunkClearsState verifies that
// an explicit empty request is a valid complete empty snapshot.
func Test_Service_UpdateStaticRoutes_ExplicitEmptyChunkClearsState(t *testing.T) {
	initial := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	store, service := newTestService(t, 10, initial)
	stream := &fakeUpdateStream{ctx: t.Context(), requests: []*sidecarpb.UpdateStaticRoutesRequest{{}}}

	require.NoError(t, service.UpdateStaticRoutes(stream))
	snapshot, initialized := store.SnapshotState()
	require.Empty(t, snapshot)
	require.True(t, initialized)
	require.NotNil(t, stream.response)
}

// Test_Service_UpdateStaticRoutes_NoRequestsPreservesState verifies that a
// client cannot accidentally clear routes by opening and closing a stream.
func Test_Service_UpdateStaticRoutes_NoRequestsPreservesState(t *testing.T) {
	initial := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	store, service := newTestService(t, 10, initial)
	stream := &fakeUpdateStream{ctx: t.Context()}

	err := service.UpdateStaticRoutes(stream)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, initial, store.Snapshot())
	require.Nil(t, stream.response)
	require.False(t, wakeReady(store.Wake()))
}

// Test_Service_UpdateStaticRoutes_InvalidChunkPreservesState verifies that a
// malformed route rejects the staged stream without exposing earlier chunks.
func Test_Service_UpdateStaticRoutes_InvalidChunkPreservesState(t *testing.T) {
	initial := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	store, service := newTestService(t, 10, initial)
	stream := &fakeUpdateStream{ctx: t.Context(), requests: []*sidecarpb.UpdateStaticRoutesRequest{
		{Routes: []*sidecarpb.StaticRoute{
			mustProtoRoute(t, "198.51.100.0/24", "198.51.100.1", "kni1"),
		}},
		{Routes: []*sidecarpb.StaticRoute{{
			Nexthop:   commonpb.NewIPAddressFromAddr(netip.MustParseAddr("203.0.113.1")),
			Interface: "kni2",
		}}},
	}}

	err := service.UpdateStaticRoutes(stream)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	snapshot, initialized := store.SnapshotState()
	require.Equal(t, initial, snapshot)
	require.True(t, initialized)
	require.Nil(t, stream.response)
	require.False(t, wakeReady(store.Wake()))
}

// Test_Service_UpdateStaticRoutes_LimitPreservesState verifies that the total
// route limit spans chunks and rejects the stream before committing it.
func Test_Service_UpdateStaticRoutes_LimitPreservesState(t *testing.T) {
	initial := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	store, service := newTestService(t, 1, initial)
	stream := &fakeUpdateStream{ctx: t.Context(), requests: []*sidecarpb.UpdateStaticRoutesRequest{
		{Routes: []*sidecarpb.StaticRoute{
			mustProtoRoute(t, "198.51.100.0/24", "198.51.100.1", "kni1"),
		}},
		{Routes: []*sidecarpb.StaticRoute{
			mustProtoRoute(t, "203.0.113.0/24", "203.0.113.1", "kni2"),
		}},
	}}

	err := service.UpdateStaticRoutes(stream)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	snapshot, initialized := store.SnapshotState()
	require.Equal(t, initial, snapshot)
	require.True(t, initialized)
	require.Nil(t, stream.response)
	require.False(t, wakeReady(store.Wake()))
}

// Test_Service_UpdateStaticRoutes_ReceiveErrorPreservesState verifies that an
// interrupted stream cannot commit the valid chunks received before failure.
func Test_Service_UpdateStaticRoutes_ReceiveErrorPreservesState(t *testing.T) {
	initial := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	store, service := newTestService(t, 10, initial)
	receiveError := errors.New("connection reset")
	stream := &fakeUpdateStream{
		ctx: t.Context(),
		requests: []*sidecarpb.UpdateStaticRoutesRequest{{
			Routes: []*sidecarpb.StaticRoute{
				mustProtoRoute(t, "198.51.100.0/24", "198.51.100.1", "kni1"),
			},
		}},
		receiveError: receiveError,
	}

	err := service.UpdateStaticRoutes(stream)
	require.ErrorIs(t, err, receiveError)
	snapshot, initialized := store.SnapshotState()
	require.Equal(t, initial, snapshot)
	require.True(t, initialized)
	require.Nil(t, stream.response)
	require.False(t, wakeReady(store.Wake()))
}

// Test_Service_UpdateStaticRoutes_CanceledBeforeCommitPreservesState verifies
// that a canceled or expired stream cannot replace or clear the prior snapshot.
func Test_Service_UpdateStaticRoutes_CanceledBeforeCommitPreservesState(t *testing.T) {
	tests := []struct {
		name          string
		timeout       time.Duration
		clearSnapshot bool
		expectedCode  codes.Code
	}{
		{
			name:         "canceled replacement",
			timeout:      time.Hour,
			expectedCode: codes.Canceled,
		},
		{
			name:          "canceled empty snapshot",
			timeout:       time.Hour,
			clearSnapshot: true,
			expectedCode:  codes.Canceled,
		},
		{
			name:         "expired replacement",
			timeout:      -time.Second,
			expectedCode: codes.DeadlineExceeded,
		},
		{
			name:          "expired empty snapshot",
			timeout:       -time.Second,
			clearSnapshot: true,
			expectedCode:  codes.DeadlineExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initial := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
			store := route.NewStore()
			require.NoError(t, store.Replace(initial))
			require.True(t, wakeReady(store.Wake()))
			_, _, initialUpdate := store.SnapshotUpdate()
			service, err := route.NewService(store, route.ServiceConfig{MaxRoutes: 1})
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(t.Context(), test.timeout)
			defer cancel()
			request := &sidecarpb.UpdateStaticRoutesRequest{}
			if !test.clearSnapshot {
				request.Routes = []*sidecarpb.StaticRoute{
					mustProtoRoute(t, "198.51.100.0/24", "198.51.100.1", "kni1"),
				}
			}
			stream := &fakeUpdateStream{
				ctx:                   ctx,
				requests:              []*sidecarpb.UpdateStaticRoutesRequest{request},
				receiveError:          io.EOF,
				beforeTerminalReceive: cancel,
			}

			err = service.UpdateStaticRoutes(stream)
			require.Equal(t, test.expectedCode, status.Code(err))
			snapshot, initialized, update := store.SnapshotUpdate()
			require.Equal(t, initial, snapshot)
			require.True(t, initialized)
			require.Same(t, initialUpdate, update)
			require.False(t, wakeReady(store.Wake()))
			require.Nil(t, stream.response)
		})
	}
}

// Test_Service_UpdateStaticRoutes_ConcurrentWriterRetriesSafely verifies that
// an update waiting for apply rejects another commit and permits retry.
func Test_Service_UpdateStaticRoutes_ConcurrentWriterRetriesSafely(t *testing.T) {
	initial := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	store := route.NewStore()
	require.NoError(t, store.Replace(initial))
	require.True(t, wakeReady(store.Wake()))
	service, err := route.NewService(store, route.ServiceConfig{MaxRoutes: 10})
	require.NoError(t, err)
	first := &fakeUpdateStream{
		ctx: t.Context(),
		requests: []*sidecarpb.UpdateStaticRoutesRequest{{
			Routes: []*sidecarpb.StaticRoute{
				mustProtoRoute(t, "198.51.100.0/24", "198.51.100.1", "kni1"),
			},
		}},
	}
	secondRequests := []*sidecarpb.UpdateStaticRoutesRequest{{
		Routes: []*sidecarpb.StaticRoute{
			mustProtoRoute(t, "203.0.113.0/24", "203.0.113.1", "kni2"),
		},
	}}
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- service.UpdateStaticRoutes(first)
	}()
	select {
	case <-store.Wake():
	case <-t.Context().Done():
		t.Fatal("first route snapshot did not commit")
	}

	second := &fakeUpdateStream{ctx: t.Context(), requests: secondRequests}
	err = service.UpdateStaticRoutes(second)
	require.Equal(t, codes.Aborted, status.Code(err))
	require.Equal(t, len(secondRequests), second.position)
	require.Nil(t, second.response)
	snapshot, initialized := store.SnapshotState()
	require.Equal(t, []route.Route{
		testRoute("198.51.100.0/24", "198.51.100.1", "kni1"),
	}, snapshot)
	require.True(t, initialized)

	_, _, update := store.SnapshotUpdate()
	update.Complete(nil)
	require.NoError(t, <-firstResult)
	require.Equal(t, []route.Route{
		testRoute("198.51.100.0/24", "198.51.100.1", "kni1"),
	}, store.Snapshot())

	retry := &fakeUpdateStream{ctx: t.Context(), requests: secondRequests}
	retryResult := make(chan error, 1)
	go func() {
		retryResult <- service.UpdateStaticRoutes(retry)
	}()
	select {
	case <-store.Wake():
	case <-t.Context().Done():
		t.Fatal("retried route snapshot did not commit")
	}
	_, _, update = store.SnapshotUpdate()
	update.Complete(nil)
	require.NoError(t, <-retryResult)
	require.NotNil(t, retry.response)
	require.Equal(t, []route.Route{
		testRoute("203.0.113.0/24", "203.0.113.1", "kni2"),
	}, store.Snapshot())
}

// Test_Service_UpdateStaticRoutes_IdleStreamDoesNotBlockCommit verifies that
// staging an idle stream does not prevent another snapshot from being applied.
func Test_Service_UpdateStaticRoutes_IdleStreamDoesNotBlockCommit(t *testing.T) {
	store, service := newTestService(t, 10, nil)
	idleStarted := make(chan struct{})
	idleContinue := make(chan struct{})
	releaseIdle := sync.OnceFunc(func() { close(idleContinue) })
	var workers errgroup.Group
	t.Cleanup(func() {
		releaseIdle()
		require.NoError(t, workers.Wait())
	})
	idle := &fakeUpdateStream{
		ctx: t.Context(),
		requests: []*sidecarpb.UpdateStaticRoutesRequest{{Routes: []*sidecarpb.StaticRoute{
			mustProtoRoute(t, "192.0.2.0/24", "192.0.2.1", "kni0"),
		}}},
		beforeFirstReceive: func() {
			close(idleStarted)
			<-idleContinue
		},
	}
	idleResult := make(chan error, 1)
	workers.Go(func() error {
		idleResult <- service.UpdateStaticRoutes(idle)
		return nil
	})
	<-idleStarted

	active := &fakeUpdateStream{ctx: t.Context(), requests: []*sidecarpb.UpdateStaticRoutesRequest{{
		Routes: []*sidecarpb.StaticRoute{
			mustProtoRoute(t, "198.51.100.0/24", "198.51.100.1", "kni1"),
		},
	}}}
	require.NoError(t, service.UpdateStaticRoutes(active))
	require.Equal(t, []route.Route{
		testRoute("198.51.100.0/24", "198.51.100.1", "kni1"),
	}, store.Snapshot())

	releaseIdle()
	require.NoError(t, <-idleResult)
}

// Test_Service_UpdateStaticRoutes_BoundsConcurrentStagedSnapshots verifies that
// fully occupied staging slots reject an extra snapshot without changing state.
func Test_Service_UpdateStaticRoutes_BoundsConcurrentStagedSnapshots(t *testing.T) {
	store := route.NewStore()
	service, err := route.NewService(store, route.ServiceConfig{
		MaxRoutes:            1,
		MaxConcurrentStreams: 2,
	})
	require.NoError(t, err)

	receiveErr := errors.New("release staged stream")
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	releaseStreams := sync.OnceFunc(func() { close(release) })
	var workers errgroup.Group
	t.Cleanup(func() {
		releaseStreams()
		require.NoError(t, workers.Wait())
	})
	for range 2 {
		stream := &fakeUpdateStream{
			ctx: t.Context(),
			requests: []*sidecarpb.UpdateStaticRoutesRequest{{Routes: []*sidecarpb.StaticRoute{
				mustProtoRoute(t, "192.0.2.0/24", "192.0.2.1", "kni0"),
			}}},
			receiveError: receiveErr,
		}
		stream.beforeTerminalReceive = func() {
			ready <- struct{}{}
			<-release
		}
		workers.Go(func() error {
			results <- service.UpdateStaticRoutes(stream)
			return nil
		})
	}
	<-ready
	<-ready

	overflow := &fakeUpdateStream{ctx: t.Context(), requests: []*sidecarpb.UpdateStaticRoutesRequest{{}}}
	err = service.UpdateStaticRoutes(overflow)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.ErrorContains(t, err, "too many static route snapshots")
	require.False(t, store.Initialized())

	releaseStreams()
	require.ErrorIs(t, <-results, receiveErr)
	require.ErrorIs(t, <-results, receiveErr)
}

// Test_Service_UpdateStaticRoutes_AdmitsBeforeReceivingFirstChunk verifies that
// an idle admitted stream consumes a slot before receiving any route data.
func Test_Service_UpdateStaticRoutes_AdmitsBeforeReceivingFirstChunk(t *testing.T) {
	store := route.NewStore()
	service, err := route.NewService(store, route.ServiceConfig{
		MaxRoutes:            1,
		MaxConcurrentStreams: 1,
	})
	require.NoError(t, err)

	receiveErr := errors.New("release idle stream")
	started := make(chan struct{})
	release := make(chan struct{})
	releaseStream := sync.OnceFunc(func() { close(release) })
	var workers errgroup.Group
	t.Cleanup(func() {
		releaseStream()
		require.NoError(t, workers.Wait())
	})
	first := &fakeUpdateStream{
		ctx:          t.Context(),
		requests:     []*sidecarpb.UpdateStaticRoutesRequest{{}},
		receiveError: receiveErr,
		beforeFirstReceive: func() {
			close(started)
			<-release
		},
	}
	firstResult := make(chan error, 1)
	workers.Go(func() error {
		firstResult <- service.UpdateStaticRoutes(first)
		return nil
	})
	<-started

	overflow := &fakeUpdateStream{ctx: t.Context()}
	err = service.UpdateStaticRoutes(overflow)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Zero(t, overflow.position)

	releaseStream()
	require.ErrorIs(t, <-firstResult, receiveErr)
}

// Test_Service_RegisterMatchesOperatorRegistrar verifies that the registration
// method has the common operator callback shape and reports the service name.
func Test_Service_RegisterMatchesOperatorRegistrar(t *testing.T) {
	_, service := newTestService(t, 1, nil)
	server := grpc.NewServer()
	defer server.Stop()
	var registrar operator.ServiceRegistrar = service.Register

	name := registrar(server)
	require.Equal(t, sidecarpb.NetlinkDataplaneService_ServiceDesc.ServiceName, name)
}

// Test_Service_ConstructorRejectsInvalidConfiguration verifies that unusable
// dependencies and nonpositive limits fail before serving requests.
func Test_Service_ConstructorRejectsInvalidConfiguration(t *testing.T) {
	service, err := route.NewService(nil, route.ServiceConfig{MaxRoutes: 1})
	require.ErrorContains(t, err, "store is nil")
	require.Nil(t, service)

	service, err = route.NewService(route.NewStore(), route.ServiceConfig{})
	require.ErrorContains(t, err, "must be positive")
	require.Nil(t, service)

	service, err = route.NewService(route.NewStore(), route.ServiceConfig{
		MaxRoutes:            1,
		MaxConcurrentStreams: -1,
	})
	require.ErrorContains(t, err, "max concurrent streams must be positive")
	require.Nil(t, service)
}

type fakeUpdateStream struct {
	ctx                   context.Context
	requests              []*sidecarpb.UpdateStaticRoutesRequest
	receiveError          error
	sendError             error
	beforeFirstReceive    func()
	beforeTerminalReceive func()
	position              int
	response              *sidecarpb.UpdateStaticRoutesResponse
}

// Recv returns staged chunks followed by the configured terminal condition.
func (m *fakeUpdateStream) Recv() (*sidecarpb.UpdateStaticRoutesRequest, error) {
	if m.position < len(m.requests) {
		if m.position == 0 && m.beforeFirstReceive != nil {
			m.beforeFirstReceive()
		}
		request := m.requests[m.position]
		m.position++
		return request, nil
	}
	if m.receiveError != nil {
		if m.beforeTerminalReceive != nil {
			beforeTerminalReceive := m.beforeTerminalReceive
			m.beforeTerminalReceive = nil
			beforeTerminalReceive()
		}
		err := m.receiveError
		m.receiveError = nil
		return nil, err
	}
	return nil, io.EOF
}

// SendAndClose captures the response or returns the configured transport error.
func (m *fakeUpdateStream) SendAndClose(response *sidecarpb.UpdateStaticRoutesResponse) error {
	if m.sendError != nil {
		return m.sendError
	}
	m.response = response
	return nil
}

// SetHeader accepts metadata because the service does not emit headers.
func (m *fakeUpdateStream) SetHeader(metadata.MD) error {
	return nil
}

// SendHeader accepts metadata because the service does not emit headers.
func (m *fakeUpdateStream) SendHeader(metadata.MD) error {
	return nil
}

// SetTrailer accepts metadata because the service does not emit trailers.
func (m *fakeUpdateStream) SetTrailer(metadata.MD) {}

// Context returns the test-owned request context for the fake stream.
func (m *fakeUpdateStream) Context() context.Context {
	return m.ctx
}

// SendMsg rejects generic sends because this RPC must use its typed close.
func (m *fakeUpdateStream) SendMsg(any) error {
	return errors.New("unexpected generic send")
}

// RecvMsg rejects generic receives because this RPC must use its typed receive.
func (m *fakeUpdateStream) RecvMsg(any) error {
	return errors.New("unexpected generic receive")
}

// newTestService returns a service whose committed snapshots are completed by
// a small fake reconciliation worker.
func newTestService(
	t *testing.T,
	maxRoutes int,
	initial []route.Route,
) (*route.Store, *route.Service) {
	t.Helper()
	store := route.NewStore()
	if initial != nil {
		require.NoError(t, store.Replace(initial))
		require.True(t, wakeReady(store.Wake()))
	}
	service, err := route.NewService(store, route.ServiceConfig{MaxRoutes: maxRoutes})
	require.NoError(t, err)
	stop := make(chan struct{})
	var workers errgroup.Group
	t.Cleanup(func() {
		close(stop)
		require.NoError(t, workers.Wait())
	})
	workers.Go(func() error {
		for {
			select {
			case <-store.Wake():
				_, _, update := store.SnapshotUpdate()
				update.Complete(nil)
			case <-stop:
				return nil
			}
		}
	})
	return store, service
}

// mustProtoRoute returns a valid static-route message for a test fixture.
func mustProtoRoute(
	t *testing.T,
	prefix,
	nexthop,
	interfaceName string,
) *sidecarpb.StaticRoute {
	t.Helper()
	wirePrefix, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix(prefix))
	require.NoError(t, err)
	return &sidecarpb.StaticRoute{
		Prefix:    wirePrefix,
		Nexthop:   commonpb.NewIPAddressFromAddr(netip.MustParseAddr(nexthop)),
		Interface: interfaceName,
	}
}

var _ grpc.ClientStreamingServer[
	sidecarpb.UpdateStaticRoutesRequest,
	sidecarpb.UpdateStaticRoutesResponse,
] = (*fakeUpdateStream)(nil)
