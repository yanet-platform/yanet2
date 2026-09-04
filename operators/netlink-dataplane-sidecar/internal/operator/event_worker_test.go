package operator_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"

	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

type activeSubscription struct {
	updates  chan<- vnetlink.NeighUpdate
	callback func(error)
}

func TestNeighbourEventWorkerWakesAndCancels(t *testing.T) {
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
			ready <- activeSubscription{updates: updates, callback: options.ErrorCallback}
			return nil
		},
		time.Hour,
	)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		result <- worker.Run(ctx)
	}()

	subscription := <-ready
	subscription.updates <- vnetlink.NeighUpdate{}
	select {
	case <-store.Wake():
	case <-time.After(time.Second):
		t.Fatal("neighbour event did not wake route source")
	}
	queued := make(chan struct{})
	go func() {
		subscription.updates <- vnetlink.NeighUpdate{}
		subscription.updates <- vnetlink.NeighUpdate{}
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

func TestNeighbourEventWorkerEmitsTrailingWakeForCoalescedEvents(t *testing.T) {
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
			ready <- activeSubscription{updates: updates, callback: options.ErrorCallback}
			return nil
		},
		20*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		result <- worker.Run(ctx)
	}()

	subscription := <-ready
	subscription.updates <- vnetlink.NeighUpdate{}
	select {
	case <-store.Wake():
	case <-time.After(time.Second):
		t.Fatal("initial neighbour event did not wake route source")
	}
	subscription.updates <- vnetlink.NeighUpdate{}
	select {
	case <-store.Wake():
	case <-time.After(time.Second):
		t.Fatal("coalesced neighbour event did not produce a trailing wake")
	}

	cancel()
	require.NoError(t, <-result)
}

func TestNeighbourEventWorkerReturnsSubscriptionFailures(t *testing.T) {
	t.Run("invalid wake interval", func(t *testing.T) {
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

		err := worker.Run(t.Context())

		require.ErrorContains(t, err, "wake interval must be positive")
	})

	t.Run("subscribe", func(t *testing.T) {
		subscribeErr := errors.New("subscribe failure")
		worker := sidecaroperator.NewNeighbourEventWorker(
			route.NewStore(),
			func(
				chan<- vnetlink.NeighUpdate,
				<-chan struct{},
				vnetlink.NeighSubscribeOptions,
			) error {
				return subscribeErr
			},
			time.Hour,
		)

		err := worker.Run(t.Context())

		require.ErrorIs(t, err, subscribeErr)
	})

	t.Run("callback", func(t *testing.T) {
		callbackErr := errors.New("callback failure")
		worker := sidecaroperator.NewNeighbourEventWorker(
			route.NewStore(),
			func(
				updates chan<- vnetlink.NeighUpdate,
				done <-chan struct{},
				options vnetlink.NeighSubscribeOptions,
			) error {
				go func() {
					<-done
					close(updates)
				}()
				options.ErrorCallback(callbackErr)
				return nil
			},
			time.Hour,
		)

		err := worker.Run(t.Context())

		require.ErrorIs(t, err, callbackErr)
	})

	t.Run("channel closed", func(t *testing.T) {
		worker := sidecaroperator.NewNeighbourEventWorker(
			route.NewStore(),
			func(
				updates chan<- vnetlink.NeighUpdate,
				_ <-chan struct{},
				_ vnetlink.NeighSubscribeOptions,
			) error {
				close(updates)
				return nil
			},
			time.Hour,
		)

		err := worker.Run(t.Context())

		require.ErrorContains(t, err, "subscription channel closed")
	})
}
