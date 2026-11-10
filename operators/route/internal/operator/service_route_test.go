package operator

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	birdadapter "github.com/yanet-platform/yanet2/operators/bird-adapter"
	adapterpb "github.com/yanet-platform/yanet2/operators/bird-adapter/adapterpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// mustNetwork builds the wire prefix message from a CIDR string.
func mustNetwork(t *testing.T, s string) *commonpb.IPPrefix {
	t.Helper()

	network, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix(s))
	require.NoError(t, err)

	return network
}

// feedRIBTestStream supplies a finite sequence of updates and accepts the
// summary sent when the stream reaches its end.
type feedRIBTestStream struct {
	grpc.ServerStream
	updates []*operatorpb.Update
	index   int
}

func (m *feedRIBTestStream) Recv() (*operatorpb.Update, error) {
	if m.index == len(m.updates) {
		return nil, io.EOF
	}

	update := m.updates[m.index]
	m.index++
	return update, nil
}

func (m *feedRIBTestStream) SendAndClose(*operatorpb.UpdateSummary) error {
	return nil
}

// channelFeedRIBStream serves updates pushed onto a channel, blocking Recv
// until one arrives or the channel is closed, so a test can interleave two
// concurrent FeedRIB calls in a controlled order.
//
// recvCount counts completed Recv calls, so a test can assert that an
// update already sent to the channel was never consumed.
type channelFeedRIBStream struct {
	grpc.ServerStream
	updates   chan *operatorpb.Update
	recvCount int
}

func (m *channelFeedRIBStream) Recv() (*operatorpb.Update, error) {
	update, ok := <-m.updates
	if !ok {
		return nil, io.EOF
	}
	m.recvCount++
	return update, nil
}

func (m *channelFeedRIBStream) SendAndClose(*operatorpb.UpdateSummary) error {
	return nil
}

// TestShowRoutes_StaticECMP_BothBest verifies that two static ECMP nexthops
// for the same prefix both carry is_best=true.
func TestShowRoutes_StaticECMP_BothBest(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	nh1 := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1"))
	nh2 := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.2"))

	_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
		Name:         "route0",
		Prefix:       mustNetwork(t, "10.0.0.0/24"),
		NexthopAddrs: []*commonpb.IPAddress{nh1, nh2},
		SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
	})
	require.NoError(t, err)

	resp, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)
	require.Len(t, resp.Routes, 2, "both ECMP nexthops must appear")
	for _, r := range resp.Routes {
		require.True(t, r.GetIsBest(), "static ECMP nexthop must have is_best=true")
	}
}

// TestShowRoutes_BirdDifferentPref_OnlyBetterIsBest verifies that when two
// bird routes for the same prefix differ in Pref, only the higher-Pref route
// carries is_best=true.
func TestShowRoutes_BirdDifferentPref_OnlyBetterIsBest(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	// Insert the lower-Pref route first so ordering is not insertion-order.
	_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
		Name:         "route0",
		Prefix:       mustNetwork(t, "10.1.0.0/24"),
		NexthopAddrs: []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.2"))},
		SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
	})
	require.NoError(t, err)

	// Use the RIB directly to insert two bird routes with distinct Prefs via
	// the operator's RIB, accessed through the service internals.
	ribRef := svc.getOrCreateRib("route0")
	sessionID := ribRef.NewSession()

	p1 := netip.MustParseAddr("192.0.2.1")
	p2 := netip.MustParseAddr("192.0.2.2")
	pfx := netip.MustParsePrefix("10.1.0.0/24")

	require.True(t, ribRef.Update(
		sessionID,
		rib.Route{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.10"), Peer: p1, SourceID: rib.RouteSourceBird, Pref: 200},
		rib.Route{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.20"), Peer: p2, SourceID: rib.RouteSourceBird, Pref: 100},
	))

	resp, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)

	// Collect is_best flags by nexthop address string for deterministic assertions.
	bestByNexthop := map[string]bool{}
	for _, r := range resp.Routes {
		addr, addrErr := r.GetNextHop().ToAddr()
		require.NoError(t, addrErr)
		bestByNexthop[addr.String()] = r.GetIsBest()
	}

	require.True(t, bestByNexthop["10.0.0.10"], "higher-Pref bird route must be best")
	require.False(t, bestByNexthop["10.0.0.20"], "lower-Pref bird route must not be best")
	require.True(t, bestByNexthop["10.0.0.2"], "static route must be best within its source")
}

// TestShowRoutes_UnknownConfig_NotFound verifies that ShowRoutes reports
// NotFound for a config name that was never registered, distinguishing it
// from a registered config that genuinely holds no routes.
func TestShowRoutes_UnknownConfig_NotFound(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	_, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestShowRoutes_EmptyConfig_Success verifies that a registered config with
// no routes still returns a normal empty success.
func TestShowRoutes_EmptyConfig_Success(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())
	svc.getOrCreateRib("route0")

	resp, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)
	require.Empty(t, resp.GetRoutes())
}

// TestLookupRoute_ThreeWay verifies that LookupRoute distinguishes an
// unknown config (NotFound) from a registered config with no matching route
// (empty success) and from a registered config with a match (the route).
func TestLookupRoute_ThreeWay(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	addr := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1"))

	t.Run("unknown config", func(t *testing.T) {
		_, err := svc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: "missing", IpAddr: addr})
		require.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("no matching route", func(t *testing.T) {
		svc.getOrCreateRib("route0")

		resp, err := svc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: "route0", IpAddr: addr})
		require.NoError(t, err)
		require.Empty(t, resp.GetRoutes())
	})

	t.Run("matching route", func(t *testing.T) {
		_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
			Name:         "route0",
			Prefix:       mustNetwork(t, "10.0.0.0/24"),
			NexthopAddrs: []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1"))},
			SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
		})
		require.NoError(t, err)

		resp, err := svc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: "route0", IpAddr: addr})
		require.NoError(t, err)
		matched, err := resp.GetPrefix().ToPrefix()
		require.NoError(t, err)
		require.Equal(t, netip.MustParsePrefix("10.0.0.0/24"), matched)
		require.Len(t, resp.GetRoutes(), 1)
	})
}

// Test_RouteService_FeedRIB_FirstNameOwnsSession verifies that the first
// update selects the RIB and later names do not redirect the session.
func Test_RouteService_FeedRIB_FirstNameOwnsSession(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	stream := &feedRIBTestStream{updates: []*operatorpb.Update{
		{
			Name: "route0",
			Route: &operatorpb.Route{
				Prefix:  mustNetwork(t, "10.0.0.0/24"),
				NextHop: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.1")),
				Peer:    commonpb.NewIPAddressFromAddr(netip.MustParseAddr("198.51.100.1")),
			},
		},
		{
			Name: "other",
			Route: &operatorpb.Route{
				Prefix:  mustNetwork(t, "10.1.0.0/24"),
				NextHop: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.2")),
				Peer:    commonpb.NewIPAddressFromAddr(netip.MustParseAddr("198.51.100.2")),
			},
		},
	}}

	require.NoError(t, svc.FeedRIB(stream))

	response, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)
	require.Len(t, response.GetRoutes(), 2)
	_, ok := svc.ribs.Get("other")
	require.False(t, ok, "a later update name must not create another session RIB")
}

// Test_RouteService_FeedRIB_HeartbeatOnlyStartsNoSession verifies that a
// stream carrying only heartbeats starts no session, creates no RIB and
// reports no change.
func Test_RouteService_FeedRIB_HeartbeatOnlyStartsNoSession(t *testing.T) {
	sessionStarts := 0
	changes := 0
	svc := NewRouteService(neigh.NewNeighTable(),
		WithRouteServiceOnRIBSessionStart(func(string, uint64) { sessionStarts++ }),
		WithRouteServiceOnChanged(func() { changes++ }),
	)

	stream := &feedRIBTestStream{updates: []*operatorpb.Update{
		{Name: "route0", Heartbeat: true},
		{Name: "route0", Heartbeat: true},
	}}
	require.NoError(t, svc.FeedRIB(stream))

	require.Zero(t, sessionStarts)
	require.Zero(t, changes)
	_, ok := svc.ribs.Get("route0")
	require.False(t, ok, "a heartbeat must not create a RIB")
}

// Test_RouteService_FeedRIB_HeartbeatChangesNoRoutes verifies that a
// heartbeat inside an active session leaves the RIB alone, even when it
// carries a route withdrawal, and that the stream keeps reading after it.
func Test_RouteService_FeedRIB_HeartbeatChangesNoRoutes(t *testing.T) {
	route := &operatorpb.Route{
		Prefix:  mustNetwork(t, "10.0.0.0/24"),
		NextHop: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.1")),
		Peer:    commonpb.NewIPAddressFromAddr(netip.MustParseAddr("198.51.100.1")),
	}
	updates := 0
	svc := NewRouteService(neigh.NewNeighTable(),
		WithRouteServiceOnRIBUpdate(func(n int) { updates += n }),
	)

	stream := &feedRIBTestStream{updates: []*operatorpb.Update{
		{Name: "route0", Route: route},
		{Name: "route0"},
		{Name: "route0", Heartbeat: true, IsDelete: true, Route: route},
		{Name: "route0", Heartbeat: true},
	}}
	require.NoError(t, svc.FeedRIB(stream))

	require.Equal(t, len(stream.updates), stream.index,
		"an active session must read on past its heartbeats",
	)
	require.Equal(t, 1, updates)
	response, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)
	require.Len(t, response.GetRoutes(), 1)
}

// Test_RouteService_FeedRIB_SupersededSessionUpdateRejected verifies that
// a superseded stream's late announce or withdraw is rejected.
//
// The route keeps the newer stream's attributes; the rejected write is not
// counted by the update callback, and the stream stops reading and ends
// cleanly instead of hanging or erroring. A cleanup pass targeting the
// superseded session afterward still leaves the route in place.
func Test_RouteService_FeedRIB_SupersededSessionUpdateRejected(t *testing.T) {
	const syncTimeout = 5 * time.Second

	cases := []struct {
		name     string
		isDelete bool
	}{
		{name: "announce"},
		{name: "withdraw", isDelete: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessionStarted := make(chan uint64, 2)
			routeApplied := make(chan struct{}, 2)
			var onUpdateCount atomic.Int64
			svc := NewRouteService(
				neigh.NewNeighTable(),
				// A long TTL keeps the service's own scheduled cleanup
				// pending instead of running during the test; Close cancels
				// the pending cleanup once the assertions below are done.
				WithRouteServiceRIBTTL(time.Hour),
				WithRouteServiceOnRIBSessionStart(func(name string, sessionID uint64) {
					sessionStarted <- sessionID
				}),
				WithRouteServiceOnRIBUpdate(func(int) {
					onUpdateCount.Add(1)
					routeApplied <- struct{}{}
				}),
			)
			defer func() {
				require.NoError(t, svc.Close())
			}()

			peer := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("198.51.100.1"))
			fromA := &operatorpb.Route{
				Prefix:  mustNetwork(t, "10.0.0.0/24"),
				NextHop: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.1")),
				Peer:    peer,
				Source:  operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
			}
			fromB := &operatorpb.Route{
				Prefix:  mustNetwork(t, "10.0.0.0/24"),
				NextHop: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.2")),
				Peer:    peer,
				Source:  operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
			}

			// streamA is buffered so the update queued after the rejected
			// one — which FeedRIB must never read — can be sent without
			// blocking the test itself.
			streamA := &channelFeedRIBStream{updates: make(chan *operatorpb.Update, 3)}
			streamB := &channelFeedRIBStream{updates: make(chan *operatorpb.Update)}

			// Closing is idempotent and also runs at cleanup, so a failed
			// require partway through cannot leave either FeedRIB call
			// blocked in Recv forever.
			closeStreamA := sync.OnceFunc(func() { close(streamA.updates) })
			closeStreamB := sync.OnceFunc(func() { close(streamB.updates) })
			t.Cleanup(closeStreamA)
			t.Cleanup(closeStreamB)

			// A and B are waited on independently, since B's stream must
			// stay open past A's own end for the assertions below.
			var groupA, groupB errgroup.Group
			groupA.Go(func() error { return svc.FeedRIB(streamA) })
			streamA.updates <- &operatorpb.Update{Name: "route0", Route: fromA}

			var sessionA uint64
			select {
			case sessionA = <-sessionStarted:
			case <-time.After(syncTimeout):
				t.Fatal("timed out waiting for A's session to start")
			}
			require.Equal(t, uint64(1), sessionA, "setup: A's session must start first")

			select {
			case <-routeApplied: // A's route is committed before B can supersede it
			case <-time.After(syncTimeout):
				t.Fatal("timed out waiting for A's route to be applied")
			}

			groupB.Go(func() error { return svc.FeedRIB(streamB) })
			streamB.updates <- &operatorpb.Update{Name: "route0", Route: fromB}

			var sessionB uint64
			select {
			case sessionB = <-sessionStarted:
			case <-time.After(syncTimeout):
				t.Fatal("timed out waiting for B's session to start")
			}
			require.Equal(t, uint64(2), sessionB, "setup: B's session must supersede A's")

			select {
			case <-routeApplied: // B's route is committed before the late send below
			case <-time.After(syncTimeout):
				t.Fatal("timed out waiting for B's route to be applied")
			}

			// A's session is already superseded by the time this reaches the
			// service, so this send is rejected by the RIB's own fence.
			streamA.updates <- &operatorpb.Update{Name: "route0", Route: fromA, IsDelete: tc.isDelete}
			// Queued after the rejected send; FeedRIB must never read it,
			// since the rejection ends the stream immediately.
			streamA.updates <- &operatorpb.Update{Name: "route0", Route: fromA}
			closeStreamA()
			require.NoError(t, groupA.Wait(), "A's FeedRIB call must end cleanly despite the rejection")

			require.Equal(t, 2, streamA.recvCount,
				"A must stop reading once its write is rejected, never reaching the queued update after it")

			ribRef, ribExists := svc.getRib("route0")
			require.True(t, ribExists, "the RIB must already exist after FeedRIB created it")
			// Run the superseded session's cleanup synchronously, standing
			// in for the service's own long-TTL scheduled pass so the
			// outcome below does not depend on winning a background race.
			ribRef.CleanupTask(sessionA, make(chan bool), 0)

			_, routes, ok := ribRef.LongestMatch(netip.MustParseAddr("10.0.0.1"))
			require.True(t, ok, "the prefix must still be present after A's rejected send and cleanup")
			require.Len(t, routes.Routes, 1,
				"A's rejected send must not change the route set, and cleanup of the superseded session must not remove B's route")
			require.Equal(t, sessionB, routes.Routes[0].SessionID, "the surviving route must keep B's session id")
			require.Equal(t, netip.MustParseAddr("192.0.2.2"), routes.Routes[0].NextHop,
				"the surviving route must keep B's attributes")

			closeStreamB()
			require.NoError(t, groupB.Wait(), "B's FeedRIB call must end cleanly")

			require.Equal(t, int64(2), onUpdateCount.Load(),
				"A's rejected write must not be counted by the update callback")
		})
	}
}

// Test_RouteService_FeedRIB_SupersededSessionNonWritingUpdateRejected
// verifies that a superseded stream's late non-writing message — a flush, a
// heartbeat or a route the converter rejects — is rejected.
//
// The rejection mirrors how a route write is rejected by the fence in
// Update: the stream ends cleanly without waking the reconcile loop, a
// further queued update on that stream is never read, and the second
// stream's own FeedRIB call still ends cleanly too.
func Test_RouteService_FeedRIB_SupersededSessionNonWritingUpdateRejected(t *testing.T) {
	const syncTimeout = 5 * time.Second

	cases := []struct {
		name          string
		unconvertible bool
		heartbeat     bool
	}{
		{name: "flush"},
		{name: "unconvertible route", unconvertible: true},
		{name: "heartbeat", heartbeat: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessionStarted := make(chan uint64, 2)
			routeApplied := make(chan struct{}, 2)
			var onChangedCount atomic.Int64
			svc := NewRouteService(
				neigh.NewNeighTable(),
				WithRouteServiceRIBTTL(time.Hour),
				WithRouteServiceOnRIBSessionStart(func(name string, sessionID uint64) {
					sessionStarted <- sessionID
				}),
				WithRouteServiceOnRIBUpdate(func(int) {
					routeApplied <- struct{}{}
				}),
				WithRouteServiceOnChanged(func() { onChangedCount.Add(1) }),
			)
			defer func() {
				require.NoError(t, svc.Close())
			}()

			peer := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("198.51.100.1"))
			fromA := &operatorpb.Route{
				Prefix:  mustNetwork(t, "10.0.0.0/24"),
				NextHop: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.1")),
				Peer:    peer,
				Source:  operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
			}
			fromB := &operatorpb.Route{
				Prefix:  mustNetwork(t, "10.0.0.0/24"),
				NextHop: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.2")),
				Peer:    peer,
				Source:  operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
			}

			lateUpdate := &operatorpb.Update{Name: "route0", Heartbeat: tc.heartbeat}
			if tc.unconvertible {
				// NextHop is left unset, which ToRIBRoute rejects.
				lateUpdate.Route = &operatorpb.Route{
					Prefix: mustNetwork(t, "10.0.0.0/24"),
					Peer:   peer,
					Source: operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
				}
			}

			// streamA is buffered so the update queued after the rejected
			// one — which FeedRIB must never read — can be sent without
			// blocking the test itself.
			streamA := &channelFeedRIBStream{updates: make(chan *operatorpb.Update, 3)}
			streamB := &channelFeedRIBStream{updates: make(chan *operatorpb.Update)}

			closeStreamA := sync.OnceFunc(func() { close(streamA.updates) })
			closeStreamB := sync.OnceFunc(func() { close(streamB.updates) })
			t.Cleanup(closeStreamA)
			t.Cleanup(closeStreamB)

			var groupA, groupB errgroup.Group
			groupA.Go(func() error { return svc.FeedRIB(streamA) })
			streamA.updates <- &operatorpb.Update{Name: "route0", Route: fromA}

			var sessionA uint64
			select {
			case sessionA = <-sessionStarted:
			case <-time.After(syncTimeout):
				t.Fatal("timed out waiting for A's session to start")
			}
			require.Equal(t, uint64(1), sessionA, "setup: A's session must start first")

			select {
			case <-routeApplied:
			case <-time.After(syncTimeout):
				t.Fatal("timed out waiting for A's route to be applied")
			}

			groupB.Go(func() error { return svc.FeedRIB(streamB) })
			streamB.updates <- &operatorpb.Update{Name: "route0", Route: fromB}

			var sessionB uint64
			select {
			case sessionB = <-sessionStarted:
			case <-time.After(syncTimeout):
				t.Fatal("timed out waiting for B's session to start")
			}
			require.Equal(t, uint64(2), sessionB, "setup: B's session must supersede A's")

			select {
			case <-routeApplied:
			case <-time.After(syncTimeout):
				t.Fatal("timed out waiting for B's route to be applied")
			}

			// From here on, onChanged must increase by exactly one: A's own
			// stream-end epilogue.
			onChangedBefore := onChangedCount.Load()

			// A's session is already superseded by the time this reaches
			// the service, so this send is the late non-writing message
			// under test.
			streamA.updates <- lateUpdate
			// Queued after the rejected send; FeedRIB must never read it.
			streamA.updates <- &operatorpb.Update{Name: "route0", Route: fromA}
			closeStreamA()
			require.NoError(t, groupA.Wait(), "A's FeedRIB call must end cleanly despite the rejected message")

			require.Equal(t, 2, streamA.recvCount,
				"A must stop reading once its message is rejected, never reaching the queued update after it")
			require.Equal(t, onChangedBefore+1, onChangedCount.Load(),
				"A's rejected message must not wake the reconcile loop itself, only its own stream-end epilogue may")

			closeStreamB()
			require.NoError(t, groupB.Wait(), "B's FeedRIB call must end cleanly")
		})
	}
}

// TestInsertRoute_MalformedPrefix_InvalidArgument verifies that a prefix
// the shared type cannot decode never reaches the RIB.
//
// Every malformed shape stays on the InvalidArgument path, and a rejected
// insert leaves no RIB behind for the named config.
func TestInsertRoute_MalformedPrefix_InvalidArgument(t *testing.T) {
	nexthops := []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1"))}

	testCases := []struct {
		name   string
		prefix *commonpb.IPPrefix
	}{
		{
			name:   "missing prefix",
			prefix: nil,
		},
		{
			name:   "unset oneof",
			prefix: &commonpb.IPPrefix{},
		},
		{
			name: "missing addr",
			prefix: &commonpb.IPPrefix{
				Prefix: &commonpb.IPPrefix_V4{V4: &commonpb.IPv4Prefix{PrefixLen: 24}},
			},
		},
		{
			name: "prefix length beyond address family",
			prefix: &commonpb.IPPrefix{
				Prefix: &commonpb.IPPrefix_V4{V4: &commonpb.IPv4Prefix{Addr: &commonpb.IPv4Address{Addr: 0x0a000000}, PrefixLen: 33}},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			svc := NewRouteService(neigh.NewNeighTable())

			_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
				Name:         "route0",
				Prefix:       testCase.prefix,
				NexthopAddrs: nexthops,
				SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))

			_, ok := svc.ribs.Get("route0")
			require.False(t, ok, "a rejected insert must not create a RIB")
		})
	}
}

// TestInsertRoute_HostBitsAreMasked verifies that host bits below the
// prefix length are masked off before the route reaches the RIB.
//
// The route is therefore stored, and reported back, under its network
// address rather than under the address the caller sent.
func TestInsertRoute_HostBitsAreMasked(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
		Name: "route0",
		// 10.0.0.7/24 with host bits deliberately left set.
		Prefix: &commonpb.IPPrefix{
			Prefix: &commonpb.IPPrefix_V4{V4: &commonpb.IPv4Prefix{Addr: &commonpb.IPv4Address{Addr: 0x0a000007}, PrefixLen: 24}},
		},
		NexthopAddrs: []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1"))},
		SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
	})
	require.NoError(t, err)

	resp, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)
	require.Len(t, resp.GetRoutes(), 1)

	prefix, err := resp.GetRoutes()[0].GetPrefix().ToPrefix()
	require.NoError(t, err)
	require.Equal(t, netip.MustParsePrefix("10.0.0.0/24"), prefix)
}

// TestShowRoutes_ConfiguredModule_NoRIBYet_Success verifies that ShowRoutes
// returns an empty success for a module the operator declares as its own
// even before any RIB exists for it, and that answering the read does not
// create a RIB or wake the reconcile loop.
func TestShowRoutes_ConfiguredModule_NoRIBYet_Success(t *testing.T) {
	var onChangedCount int
	svc := NewRouteService(
		neigh.NewNeighTable(),
		WithRouteServiceConfiguredModules("route0"),
		WithRouteServiceOnChanged(func() { onChangedCount++ }),
	)

	resp, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)
	require.Empty(t, resp.GetRoutes())

	_, ok := svc.ribs.Get("route0")
	require.False(t, ok, "answering a read for a configured module must not create a RIB")
	require.Zero(t, onChangedCount, "answering a read must not wake the reconcile loop")
}

// TestLookupRoute_ConfiguredModule_NoRIBYet_Success verifies that
// LookupRoute returns an empty success for a module the operator declares
// as its own even before any RIB exists for it, and that answering the
// read does not create a RIB or wake the reconcile loop.
func TestLookupRoute_ConfiguredModule_NoRIBYet_Success(t *testing.T) {
	var onChangedCount int
	svc := NewRouteService(
		neigh.NewNeighTable(),
		WithRouteServiceConfiguredModules("route0"),
		WithRouteServiceOnChanged(func() { onChangedCount++ }),
	)

	addr := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1"))
	resp, err := svc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: "route0", IpAddr: addr})
	require.NoError(t, err)
	require.Empty(t, resp.GetRoutes())

	_, ok := svc.ribs.Get("route0")
	require.False(t, ok, "answering a read for a configured module must not create a RIB")
	require.Zero(t, onChangedCount, "answering a read must not wake the reconcile loop")
}

// TestShowRoutesAndLookupRoute_UndeclaredConfig_NotFound verifies that a
// name outside the configured-modules set stays NotFound even when the
// set is non-empty, distinguishing it from a declared-but-unpopulated name.
func TestShowRoutesAndLookupRoute_UndeclaredConfig_NotFound(t *testing.T) {
	svc := NewRouteService(
		neigh.NewNeighTable(),
		WithRouteServiceConfiguredModules("route0"),
	)

	_, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "other"})
	require.Equal(t, codes.NotFound, status.Code(err))

	addr := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1"))
	_, err = svc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: "other", IpAddr: addr})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_RouteService_DeleteAndFlush_UnknownConfig verifies that deleting or
// flushing an undeclared config is NotFound while a declared one stays a success.
func Test_RouteService_DeleteAndFlush_UnknownConfig(t *testing.T) {
	operations := []struct {
		name string
		call func(t *testing.T, service *RouteService, config string) error
	}{
		{
			name: "DeleteRoute",
			call: func(t *testing.T, service *RouteService, config string) error {
				_, err := service.DeleteRoute(t.Context(), &operatorpb.DeleteRouteRequest{
					Name:    config,
					Prefix:  mustNetwork(t, "10.0.0.0/24"),
					DoFlush: true,
				})
				return err
			},
		},
		{
			name: "FlushRoutes",
			call: func(t *testing.T, service *RouteService, config string) error {
				_, err := service.FlushRoutes(t.Context(), &operatorpb.FlushRoutesRequest{Name: config})
				return err
			},
		},
	}
	cases := []struct {
		name   string
		config string
		code   codes.Code
	}{
		{name: "configured module without RIB", config: "route0", code: codes.OK},
		{name: "undeclared config", config: "other", code: codes.NotFound},
	}

	for _, operation := range operations {
		for _, tc := range cases {
			t.Run(operation.name+"/"+tc.name, func(t *testing.T) {
				var onChangedCount int
				service := NewRouteService(
					neigh.NewNeighTable(),
					WithRouteServiceConfiguredModules("route0"),
					WithRouteServiceOnChanged(func() { onChangedCount++ }),
				)

				err := operation.call(t, service, tc.config)
				require.Equal(t, tc.code, status.Code(err))

				_, ok := service.ribs.Get(tc.config)
				require.False(t, ok, "a delete or flush must not create a RIB")
				require.Zero(t, onChangedCount, "a delete or flush without a RIB must not wake the reconcile loop")
			})
		}
	}
}

// TestNewOperator_ConfiguredModuleNoRIBYet_ReadsSucceedWithoutCreatingRIB
// verifies that an Operator built by NewOperator answers ShowRoutes and
// LookupRoute for its configured module with an empty success before any
// RIB exists for it, and that neither read creates one.
func TestNewOperator_ConfiguredModuleNoRIBYet_ReadsSucceedWithoutCreatingRIB(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NetlinkMonitor.Disabled = true

	op, err := NewOperator(cfg)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, op.Close())
	}()

	moduleName := cfg.Function.Module.Unwrap()

	showResp, err := op.routeSvc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: moduleName})
	require.NoError(t, err)
	require.Empty(t, showResp.GetRoutes())

	addr := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1"))
	lookupResp, err := op.routeSvc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: moduleName, IpAddr: addr})
	require.NoError(t, err)
	require.Empty(t, lookupResp.GetRoutes())

	_, ok := op.routeSvc.ribs.Get(moduleName)
	require.False(t, ok, "answering reads for the configured module must not create a RIB")
}

// TestListConfigs_ConfiguredModule_ReportedOnceBeforeAndAfterRIB verifies
// that a configured module name appears in ListConfigs before its RIB is
// created, still appears exactly once after the RIB is created, and that
// the full result is always lexicographically sorted regardless of the
// order configs and RIBs were added in.
func TestListConfigs_ConfiguredModule_ReportedOnceBeforeAndAfterRIB(t *testing.T) {
	svc := NewRouteService(
		neigh.NewNeighTable(),
		WithRouteServiceConfiguredModules("route0"),
	)

	resp, err := svc.ListConfigs(t.Context(), &operatorpb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"route0"}, resp.GetConfigs())

	svc.getOrCreateRib("route0")
	svc.getOrCreateRib("route2")
	svc.getOrCreateRib("route1")

	resp, err = svc.ListConfigs(t.Context(), &operatorpb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"route0", "route1", "route2"}, resp.GetConfigs())
}

// Test_RouteService_FeedRIB_IdleBirdImportReplaysAfterTransportRestart
// verifies that an idle BIRD import replays its table into the RIB after the
// route operator's gRPC server restarts: the replayed route stays, the route
// withdrawn during the outage leaves exactly one RIB TTL after the old
// session ended and the bird-session scope returns to READY.
func Test_RouteService_FeedRIB_IdleBirdImportReplaysAfterTransportRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kept := netip.MustParsePrefix("2001:db8:1::/48")
		withdrawn := netip.MustParsePrefix("2001:db8:2::/48")
		network := newMemoryNetwork()
		// BIRD exports both routes to the first connection; the second one
		// was withdrawn by the time the import reconnects.
		serveBIRDExportSocket(t, network.Listen("bird.sock"), func(idx int) []netip.Prefix {
			if idx == 0 {
				return []netip.Prefix{kept, withdrawn}
			}
			return []netip.Prefix{kept}
		})

		store := newRIBStore()
		tracker := readiness.NewTracker(specs("bird-session", "rib"))
		sessions := newBirdRIBReadiness(ReadinessConfig{
			RateThreshold:   1000,
			StabilityWindow: 50 * time.Millisecond,
			SampleInterval:  20 * time.Millisecond,
			ReconnectGrace:  time.Minute,
		}, store, "route0", tracker)
		svc := NewRouteService(neigh.NewNeighTable(),
			WithRouteServiceRIBStore(store),
			WithRouteServiceRIBTTL(DefaultRIBTTL),
			WithRouteServiceOnRIBSessionStart(sessions.OnSessionStart),
			WithRouteServiceOnRIBUpdate(sessions.OnUpdate),
			WithRouteServiceOnRIBSessionEnd(sessions.OnSessionEnd),
		)
		t.Cleanup(func() { require.NoError(t, svc.Close()) })

		server := serveFeedRIB(t, network.Listen("route-operator"), svc)

		adapter := birdadapter.NewAdapterService("passthrough:///route-operator",
			birdadapter.WithRouteOperatorDialOptions(
				grpc.WithContextDialer(network.Dial),
				// The zstd codec pools its decoders process-wide, and a
				// decoder made in one bubble must never run in another.
				grpc.WithDefaultCallOptions(grpc.UseCompressor(encoding.Identity)),
			),
			birdadapter.WithBIRDDialer(network.Dial),
		)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
			defer cancel()
			require.NoError(t, adapter.Close(ctx))
		})
		sourceV4, err := commonpb.NewIPv4AddressFromAddr(netip.MustParseAddr("192.0.2.1"))
		require.NoError(t, err)
		sourceV6, err := commonpb.NewIPv6AddressFromAddr(netip.MustParseAddr("2001:db8::1"))
		require.NoError(t, err)
		_, err = adapter.SetupConfig(t.Context(), &adapterpb.SetupConfigRequest{
			Name:     "route0",
			Config:   &adapterpb.ImportConfig{Sockets: []string{"bird.sock"}, DumpThreshold: 1},
			SourceV4: sourceV4,
			SourceV6: sourceV6,
		})
		require.NoError(t, err)

		birdSessionState := func() readinesspb.State {
			return requireScope(t, tracker, "bird-session").GetState()
		}
		require.Eventually(t, func() bool {
			return slices.Equal(ribPrefixes(store, "route0"), []netip.Prefix{kept, withdrawn})
		}, 5*time.Second, 10*time.Millisecond)
		require.Equal(t, readinesspb.State_STATE_READY, birdSessionState())

		server.Stop()
		synctest.Wait()
		sessionEnded := time.Now()
		require.NotEqual(t, readinesspb.State_STATE_READY, birdSessionState())
		serveFeedRIB(t, network.Listen("route-operator"), svc)

		require.Eventually(t, func() bool { return birdSessionState() == readinesspb.State_STATE_READY },
			5*time.Second, 10*time.Millisecond,
			"the idle import did not start a new session",
		)
		time.Sleep(time.Until(sessionEnded.Add(DefaultRIBTTL)) - time.Nanosecond)
		synctest.Wait()
		require.Equal(t, []netip.Prefix{kept, withdrawn}, ribPrefixes(store, "route0"),
			"the withdrawn route must stay until the RIB TTL",
		)
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		require.Equal(t, []netip.Prefix{kept}, ribPrefixes(store, "route0"),
			"the RIB must hold the replayed route and lose the withdrawn one after the TTL",
		)
	})
}

// serveFeedRIB serves svc on listener until the test ends and returns the
// gRPC server, so the test may stop it earlier.
func serveFeedRIB(t *testing.T, listener net.Listener, svc *RouteService) *grpc.Server {
	t.Helper()
	server := grpc.NewServer()
	operatorpb.RegisterRouteServiceServer(server, svc)
	var workers errgroup.Group
	workers.Go(func() error { return server.Serve(listener) })
	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, workers.Wait())
	})
	return server
}

// ribPrefixes returns the sorted prefixes the named RIB holds routes for.
func ribPrefixes(store *RIBStore, name string) []netip.Prefix {
	ribRef, ok := store.Get(name)
	if !ok {
		return nil
	}
	var prefixes []netip.Prefix
	dump := ribRef.DumpRoutes()
	for bits := range dump {
		for prefix, routes := range dump[bits] {
			if len(routes.Routes) > 0 {
				prefixes = append(prefixes, prefix)
			}
		}
	}
	slices.SortFunc(prefixes, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
	return prefixes
}

// serveBIRDExportSocket serves a BIRD export socket on listener that
// announces the prefixes routes gives for each accepted connection's ordinal
// via 2001:db8::1, then stays quiet until the test ends.
//
// Every prefix must be a /48 under 2001:db8::/32.
func serveBIRDExportSocket(t *testing.T, listener net.Listener, routes func(idx int) []netip.Prefix) {
	t.Helper()
	var workers errgroup.Group
	workers.Go(func() error {
		for idx := 0; ; idx++ {
			connection, err := listener.Accept()
			if err != nil {
				return nil
			}
			workers.Go(func() error {
				defer connection.Close()
				stop := context.AfterFunc(t.Context(), func() { _ = connection.Close() })
				defer stop()
				for _, prefix := range routes(idx) {
					if _, err := connection.Write(birdExportChunk(prefix)); err != nil {
						return err
					}
				}
				_, _ = io.Copy(io.Discard, connection)
				return nil
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		require.NoError(t, workers.Wait())
	})
}

// memoryNetwork connects dialers to the bufconn listeners bound on it by
// address, whose connections block on condition variables: a goroutine
// waiting on one counts as durably blocked inside a synctest bubble, which a
// goroutine waiting on a socket never does.
type memoryNetwork struct {
	mu        sync.Mutex
	listeners map[string]*bufconn.Listener
}

func newMemoryNetwork() *memoryNetwork {
	return &memoryNetwork{listeners: map[string]*bufconn.Listener{}}
}

// Listen binds a new listener to address in place of the previous one.
func (m *memoryNetwork) Listen(address string) *bufconn.Listener {
	m.mu.Lock()
	defer m.mu.Unlock()

	listener := bufconn.Listen(64 * 1024)
	m.listeners[address] = listener
	return listener
}

// Dial connects to the listener bound to address, and fails as a refused
// connection does when none is.
func (m *memoryNetwork) Dial(ctx context.Context, address string) (net.Conn, error) {
	m.mu.Lock()
	listener := m.listeners[address]
	m.mu.Unlock()

	if listener == nil {
		return nil, &net.OpError{Op: "dial", Net: "memory", Err: syscall.ECONNREFUSED}
	}
	return listener.DialContext(ctx)
}

// birdExportChunk returns a BIRD export chunk announcing the /48 prefix via
// 2001:db8::1.
//
// BIRD lays every 32-bit word of an address out in host byte order.
func birdExportChunk(prefix netip.Prefix) []byte {
	addr := prefix.Addr().As16()
	update := []byte{
		0: 92,
		4: 2, 5: 48, 6: 20,
		8: addr[3], 9: addr[2], 10: addr[1], 11: addr[0],
		12: addr[7], 13: addr[6], 14: addr[5], 15: addr[4],
		44: 1,
		68: 24,
		72: 3, 73: 4, 76: 16,
		80: 0xb8, 81: 0x0d, 82: 1, 83: 0x20,
		92: 1, 95: 0,
	}
	binary.LittleEndian.PutUint32(update[:4], uint32(len(update)-4))
	return update
}
