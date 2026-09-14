package operator_test

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
)

// testSubscriber models the upstream sender, including non-cancellable sends.
func testSubscriber(events <-chan vnetlink.NeighUpdate) sidecaroperator.NeighbourSubscriber {
	return func(updates chan<- vnetlink.NeighUpdate, done <-chan struct{}, options vnetlink.NeighSubscribeOptions) error {
		go func() {
			defer close(updates)
			for {
				select {
				case <-done:
					return
				case update := <-events:
					updates <- update
				}
			}
		}()
		return nil
	}
}

// awaitWake bounds waiting for a real collector notification.
func awaitWake(t *testing.T, ctx context.Context, source *sidecaroperator.Source) {
	t.Helper()
	select {
	case <-source.Wake():
	case <-ctx.Done():
		t.Fatal("neighbour watcher did not request collection")
	}
}

// Test_WatchNeighbours_EventTypes verifies startup coverage, immediate NEW
// notification and absence of DEL/unknown notifications.
func Test_WatchNeighbours_EventTypes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	source := sidecaroperator.NewSource(sidecaroperator.State{})
	events := make(chan vnetlink.NeighUpdate)
	stopped := make(chan error, 1)
	go func() { stopped <- sidecaroperator.WatchNeighbours(ctx, source, testSubscriber(events)) }()
	t.Cleanup(func() { cancel(); require.ErrorIs(t, <-stopped, context.Canceled) })
	awaitWake(t, ctx, source)
	for _, kind := range []uint16{unix.RTM_DELNEIGH, unix.RTM_DELNEIGH, unix.RTM_GETNEIGH} {
		select {
		case events <- vnetlink.NeighUpdate{Type: kind}:
		case <-ctx.Done():
			t.Fatal("event delivery blocked")
		}
	}
	require.Never(t, func() bool { return len(source.Wake()) != 0 }, 30*time.Millisecond, time.Millisecond)
	select {
	case events <- vnetlink.NeighUpdate{Type: unix.RTM_NEWNEIGH}:
	case <-ctx.Done():
		t.Fatal("event delivery blocked")
	}
	awaitWake(t, ctx, source)
}

// Test_WatchNeighbours_Termination verifies that failures reach the supervisor,
// closed channels cannot spin, and cancellation drains a blocked upstream send.
func Test_WatchNeighbours_Termination(t *testing.T) {
	injected := errors.New("event socket failed")
	for _, name := range []string{"subscribe error", "callback error", "closed channel", "cancel blocked sender"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			subscribe := func(updates chan<- vnetlink.NeighUpdate, done <-chan struct{}, options vnetlink.NeighSubscribeOptions) error {
				switch name {
				case "subscribe error":
					return injected
				case "callback error":
					options.ErrorCallback(injected)
					options.ErrorCallback(injected)
					close(updates)
				case "closed channel":
					close(updates)
				case "cancel blocked sender":
					updates <- vnetlink.NeighUpdate{}
					go func() {
						updates <- vnetlink.NeighUpdate{}
						<-done
						close(updates)
					}()
					cancel()
				}
				return nil
			}
			source := sidecaroperator.NewSource(sidecaroperator.State{})
			err := sidecaroperator.WatchNeighbours(ctx, source, subscribe)
			switch name {
			case "cancel blocked sender":
				require.ErrorIs(t, err, context.Canceled)
			case "closed channel":
				require.ErrorContains(t, err, "update channel closed")
			default:
				require.ErrorIs(t, err, injected)
			}
		})
	}
}

// Test_WatchNeighbours_Netns verifies additions and state changes wake collection
// while a deletion event alone does not trigger an immediate withdrawal.
func Test_WatchNeighbours_Netns(t *testing.T) {
	if os.Getenv("YANET_NETNS_TESTS") != "1" {
		t.Skip("requires a disposable network namespace")
	}
	link := newKernelTAP(t, "kni8")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	source := sidecaroperator.NewSource(sidecaroperator.State{})
	stopped := make(chan error, 1)
	go func() { stopped <- sidecaroperator.WatchNeighbours(ctx, source, vnetlink.NeighSubscribeWithOptions) }()
	t.Cleanup(func() { cancel(); require.ErrorIs(t, <-stopped, context.Canceled) })
	awaitWake(t, ctx, source)
	entry := &vnetlink.Neigh{LinkIndex: link.Attrs().Index, IP: net.ParseIP("192.0.2.1"), HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, 1}, State: vnetlink.NUD_PERMANENT}
	require.NoError(t, vnetlink.NeighSet(entry))
	awaitWake(t, ctx, source)
	// Kernel deletion can first emit a failed-state update; observe it separately.
	entry.State = vnetlink.NUD_FAILED
	require.NoError(t, vnetlink.NeighSet(entry))
	awaitWake(t, ctx, source)
	require.NoError(t, vnetlink.NeighDel(entry))
	require.Never(t, func() bool { return len(source.Wake()) != 0 }, 30*time.Millisecond, time.Millisecond)
}
