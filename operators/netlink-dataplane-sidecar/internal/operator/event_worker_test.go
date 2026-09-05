package operator_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/sys/unix"

	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

// activeSubscription exposes a fake event stream without opening kernel sockets.
type activeSubscription struct {
	Updates  chan<- vnetlink.NeighUpdate
	Callback func(error)
	Done     <-chan struct{}
	End      chan struct{}
	Stopped  <-chan struct{}
}

// Test_NeighbourEventWorker_WakesAndCancels verifies that throttled events are
// consumed continuously and cancellation drains the subscription.
func Test_NeighbourEventWorker_WakesAndCancels(t *testing.T) {
	store := route.NewStore()
	ready := make(chan activeSubscription, 1)
	worker := sidecaroperator.NewNeighbourEventWorker(
		store,
		func(
			updates chan<- vnetlink.NeighUpdate,
			done <-chan struct{},
			options vnetlink.NeighSubscribeOptions,
		) error {
			go func() {
				<-done
				close(updates)
			}()
			ready <- activeSubscription{Updates: updates, Callback: options.ErrorCallback}
			return nil
		},
		time.Hour,
	)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- worker.Run(ctx)
	}()

	subscription := <-ready
	waitNeighbourWake(t, store)
	subscription.Updates <- vnetlink.NeighUpdate{}
	select {
	case <-store.Wake():
	case <-time.After(time.Second):
		t.Fatal("neighbour event did not wake route source")
	}
	queued := make(chan struct{})
	go func() {
		subscription.Updates <- vnetlink.NeighUpdate{}
		subscription.Updates <- vnetlink.NeighUpdate{}
		close(queued)
	}()
	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("event worker stopped consuming rate-limited events")
	}
	select {
	case <-store.Wake():
		t.Fatal("rate-limited neighbour event unexpectedly woke route source")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("event worker did not stop after cancellation")
	}
}

// Test_NeighbourEventWorker_EmitsTrailingWake verifies that the final event in
// a throttled burst still produces a later full reconciliation.
func Test_NeighbourEventWorker_EmitsTrailingWake(t *testing.T) {
	store := route.NewStore()
	ready := make(chan activeSubscription, 1)
	worker := sidecaroperator.NewNeighbourEventWorker(
		store,
		func(
			updates chan<- vnetlink.NeighUpdate,
			done <-chan struct{},
			options vnetlink.NeighSubscribeOptions,
		) error {
			go func() {
				<-done
				close(updates)
			}()
			ready <- activeSubscription{Updates: updates, Callback: options.ErrorCallback}
			return nil
		},
		20*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- worker.Run(ctx)
	}()

	subscription := <-ready
	waitNeighbourWake(t, store)
	subscription.Updates <- vnetlink.NeighUpdate{}
	select {
	case <-store.Wake():
	case <-time.After(time.Second):
		t.Fatal("initial neighbour event did not wake route source")
	}
	subscription.Updates <- vnetlink.NeighUpdate{}
	select {
	case <-store.Wake():
	case <-time.After(time.Second):
		t.Fatal("coalesced neighbour event did not produce a trailing wake")
	}

	cancel()
	waitNeighbourWorkerStopped(t, result)
}

// Test_NeighbourEventWorker_RejectsInvalidInterval verifies that invalid timing
// is rejected before attempting to open a subscription.
func Test_NeighbourEventWorker_RejectsInvalidInterval(t *testing.T) {
	worker := sidecaroperator.NewNeighbourEventWorker(
		route.NewStore(),
		func(
			chan<- vnetlink.NeighUpdate,
			<-chan struct{},
			vnetlink.NeighSubscribeOptions,
		) error {
			t.Fatal("subscriber called with invalid wake interval")
			return nil
		},
		0,
	)

	require.ErrorContains(t, worker.Run(t.Context()), "wake interval must be positive")
}

// Test_NeighbourEventWorker_RetriesFailedSubscription verifies that an initial
// setup failure is retried and recovery requests a full snapshot without events.
func Test_NeighbourEventWorker_RetriesFailedSubscription(t *testing.T) {
	store := route.NewStore()
	attempts := 0
	var firstDone <-chan struct{}
	worker := sidecaroperator.NewNeighbourEventWorker(
		store,
		func(
			updates chan<- vnetlink.NeighUpdate,
			done <-chan struct{},
			options vnetlink.NeighSubscribeOptions,
		) error {
			attempts++
			if attempts == 1 {
				firstDone = done
				return unix.ENOBUFS
			}
			go func() {
				<-done
				close(updates)
			}()
			return nil
		},
		10*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- worker.Run(ctx) }()

	waitNeighbourWake(t, store)
	cancel()
	waitNeighbourWorkerStopped(t, result)
	require.Equal(t, 2, attempts)
	select {
	case <-firstDone:
	default:
		t.Fatal("failed subscription was not stopped")
	}
}

// Test_NeighbourEventWorker_ResubscribesAfterLoss verifies that callback errors
// and closed streams are drained before replacement, with a full recovery wake.
func Test_NeighbourEventWorker_ResubscribesAfterLoss(t *testing.T) {
	for _, failure := range []string{"callback error", "channel closure"} {
		t.Run(failure, func(t *testing.T) {
			store := route.NewStore()
			require.NoError(t, store.Replace(nil))
			<-store.Wake()
			ready := make(chan activeSubscription, 2)
			overlap := make(chan struct{}, 1)
			var previous activeSubscription
			worker := sidecaroperator.NewNeighbourEventWorker(
				store,
				func(
					updates chan<- vnetlink.NeighUpdate,
					done <-chan struct{},
					options vnetlink.NeighSubscribeOptions,
				) error {
					if previous.Done != nil {
						select {
						case <-previous.Done:
						default:
							overlap <- struct{}{}
						}
						select {
						case <-previous.Stopped:
						default:
							select {
							case overlap <- struct{}{}:
							default:
							}
						}
					}
					end := make(chan struct{})
					stopped := make(chan struct{})
					go func() {
						select {
						case <-done:
							// Model pending unconditional upstream sends during cleanup.
							updates <- vnetlink.NeighUpdate{}
							updates <- vnetlink.NeighUpdate{}
						case <-end:
						}
						close(stopped)
						close(updates)
					}()
					previous = activeSubscription{
						Updates: updates, Callback: options.ErrorCallback,
						Done: done, End: end, Stopped: stopped,
					}
					ready <- previous
					return nil
				},
				10*time.Millisecond,
			)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- worker.Run(ctx) }()

			first := waitNeighbourSubscription(t, ready)
			waitNeighbourWake(t, store)
			if failure == "callback error" {
				first.Callback(unix.ENOBUFS)
			} else {
				close(first.End)
			}
			second := waitNeighbourSubscription(t, ready)
			waitNeighbourWake(t, store)
			require.True(t, store.Initialized())
			second.Updates <- vnetlink.NeighUpdate{}
			waitNeighbourWake(t, store)
			cancel()
			waitNeighbourWorkerStopped(t, result)
			require.Empty(t, overlap)
		})
	}
}

// Test_NeighbourEventWorker_CancelsRetry verifies that setup and callback
// failures remain logged while cancellation interrupts a long retry delay.
func Test_NeighbourEventWorker_CancelsRetry(t *testing.T) {
	for _, failure := range []string{"setup error", "callback error"} {
		t.Run(failure, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			attempts := 0
			worker := sidecaroperator.NewNeighbourEventWorker(
				route.NewStore(),
				func(
					updates chan<- vnetlink.NeighUpdate,
					done <-chan struct{},
					options vnetlink.NeighSubscribeOptions,
				) error {
					attempts++
					if failure == "setup error" {
						return unix.ENOBUFS
					}
					go func() {
						<-done
						close(updates)
					}()
					options.ErrorCallback(unix.ENOBUFS)
					return nil
				},
				time.Hour,
				sidecaroperator.WithNeighbourEventWorkerLog(zap.New(core)),
			)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- worker.Run(ctx) }()

			require.Eventually(t, func() bool { return logs.Len() == 1 }, time.Second, time.Millisecond)
			cancel()
			waitNeighbourWorkerStopped(t, result)
			require.Equal(t, 1, attempts)
		})
	}
}

// waitNeighbourWake bounds a full-reconciliation notification wait.
func waitNeighbourWake(t *testing.T, store *route.Store) {
	t.Helper()
	select {
	case <-store.Wake():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for neighbour reconciliation wake")
	}
}

// waitNeighbourSubscription bounds a fake subscription establishment wait.
func waitNeighbourSubscription(t *testing.T, ready <-chan activeSubscription) activeSubscription {
	t.Helper()
	select {
	case subscription := <-ready:
		return subscription
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for neighbour subscription")
		return activeSubscription{}
	}
}

// waitNeighbourWorkerStopped verifies bounded, clean worker termination.
func waitNeighbourWorkerStopped(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("neighbour worker did not stop after cancellation")
	}
}
