package operator

import (
	"context"
	"errors"
	"fmt"

	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// NeighbourSubscriber owns sending and closing the update channel on success.
//
// Closing the stop channel terminates reception; pending sends must be drained.
type NeighbourSubscriber func(chan<- vnetlink.NeighUpdate, <-chan struct{}, vnetlink.NeighSubscribeOptions) error

// WatchNeighbours wakes full collection on additions and changes, not deletions.
//
// Subscription failures stop the operator for supervisor restart. Deletions are
// observed by a later full refresh, avoiding an immediate withdrawal on flaps.
func WatchNeighbours(ctx context.Context, source *Source, subscribe NeighbourSubscriber) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if source == nil || subscribe == nil {
		return errors.New("watch neighbours: source and subscriber are required")
	}
	updates := make(chan vnetlink.NeighUpdate, 1)
	done := make(chan struct{})
	failures := make(chan error, 1)
	options := vnetlink.NeighSubscribeOptions{ErrorCallback: func(err error) {
		if err != nil {
			select {
			case failures <- err:
			default:
			}
		}
	}}
	if err := subscribe(updates, done, options); err != nil {
		close(done)
		return fmt.Errorf("subscribe to neighbour updates: %w", err)
	}
	defer func() {
		close(done)
		// The upstream sender can be blocked on delivery when its socket closes.
		for range updates {
		}
	}()
	// Cover the gap between the initial dump and opening the event socket.
	source.Notify()
	for {
		var failure error
		select {
		case <-ctx.Done():
			return ctx.Err()
		case failure = <-failures:
		case update, open := <-updates:
			if open {
				if update.Type == unix.RTM_NEWNEIGH {
					source.Notify()
				}
				continue
			}
			failure = errors.New("update channel closed")
			select {
			case failure = <-failures:
			default:
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("watch neighbours: %w", failure)
	}
}
