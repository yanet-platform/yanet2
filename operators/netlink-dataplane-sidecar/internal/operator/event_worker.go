package operator

import (
	"context"
	"errors"
	"fmt"
	"time"

	vnetlink "github.com/vishvananda/netlink"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

const subscriptionDrainTimeout = time.Second

// NeighbourSubscriber is the injectable shape of
// netlink.NeighSubscribeWithOptions.
type NeighbourSubscriber func(
	chan<- vnetlink.NeighUpdate,
	<-chan struct{},
	vnetlink.NeighSubscribeOptions,
) error

// NeighbourEventWorker wakes full reconciliation after netlink events without
// allowing a continuous event stream to bypass reconciliation backoff.
type NeighbourEventWorker struct {
	store        *route.Store
	subscribe    NeighbourSubscriber
	wakeInterval time.Duration
}

// NewNeighbourEventWorker creates an event-driven wake worker.
func NewNeighbourEventWorker(
	store *route.Store,
	subscribe NeighbourSubscriber,
	wakeInterval time.Duration,
) *NeighbourEventWorker {
	return &NeighbourEventWorker{
		store:        store,
		subscribe:    subscribe,
		wakeInterval: wakeInterval,
	}
}

// Run subscribes until cancellation or any subscription failure.
func (m *NeighbourEventWorker) Run(ctx context.Context) error {
	if m.store == nil {
		return errors.New("run neighbour event worker: route store is nil")
	}
	if m.subscribe == nil {
		return errors.New("run neighbour event worker: subscriber is nil")
	}
	if m.wakeInterval <= 0 {
		return errors.New("run neighbour event worker: wake interval must be positive")
	}

	updates := make(chan vnetlink.NeighUpdate, 1)
	done := make(chan struct{})
	callbackErrors := make(chan error, 1)
	options := vnetlink.NeighSubscribeOptions{
		ErrorCallback: func(err error) {
			select {
			case callbackErrors <- err:
			default:
			}
		},
	}
	if err := m.subscribe(updates, done, options); err != nil {
		close(done)
		return fmt.Errorf("subscribe to neighbour events: %w", err)
	}

	var wakeTimer *time.Timer
	var wakeTimerC <-chan time.Time
	pendingWake := false
	defer func() {
		if wakeTimer != nil {
			wakeTimer.Stop()
		}
		close(done)
		// The upstream sender uses an unconditional channel send. Draining until
		// it closes prevents that goroutine from being stranded during teardown.
		// A broken injected subscriber cannot block operator shutdown forever.
		drainTimer := time.NewTimer(subscriptionDrainTimeout)
		defer drainTimer.Stop()
		for {
			select {
			case _, open := <-updates:
				if !open {
					return
				}
			case <-drainTimer.C:
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-callbackErrors:
			if err == nil {
				return errors.New("neighbour event subscription callback returned a nil error")
			}
			return fmt.Errorf("neighbour event subscription failed: %w", err)
		case _, open := <-updates:
			if !open {
				if ctx.Err() != nil {
					return nil
				}
				select {
				case err := <-callbackErrors:
					if err != nil {
						return fmt.Errorf("neighbour event subscription failed: %w", err)
					}
				default:
				}
				return errors.New("neighbour event subscription channel closed")
			}
			if wakeTimerC != nil {
				pendingWake = true
				continue
			}
			m.store.Notify()
			if wakeTimer == nil {
				wakeTimer = time.NewTimer(m.wakeInterval)
			} else {
				wakeTimer.Reset(m.wakeInterval)
			}
			wakeTimerC = wakeTimer.C
		case <-wakeTimerC:
			if !pendingWake {
				wakeTimerC = nil
				continue
			}
			m.store.Notify()
			pendingWake = false
			wakeTimer.Reset(m.wakeInterval)
		}
	}
}
