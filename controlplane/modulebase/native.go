package modulebase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/xbackoff"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// readyTimeout bounds the wait for every dataplane instance of the storage
// to be ready when no deadline is given, as long as the director waits
// for its own.
const readyTimeout = 60 * time.Second

// nativeAttachment is an agent attached without cgo together with the
// mapping of the storage it lives in.
type nativeAttachment struct {
	Agent *agent.Agent

	storage *zone.Storage
}

// attachNative maps the storage the config names and attaches the named
// agent to its dataplane instance without cgo, as the cgo attach does
// through C.
//
// A failed attach unmaps the storage again, so an error leaves nothing
// behind for the caller to release. A bus error on the mapping is left to
// the process's handler, which the director installs through its own cgo
// attach before any module attaches.
//
// The storage maps only once every instance is ready. The dataplane marks
// its instances ready one after another once all are set up, so a module
// attaching as soon as its director's instance is ready waits for the
// rest, within the bound.
func attachNative(cfg ffi.AttachConfig, name string, deadline time.Time, log *zap.Logger) (*nativeAttachment, error) {
	path := cfg.MemoryPath.Unwrap()
	instanceID := cfg.InstanceID.Unwrap()
	log.Debug("mapping shared memory",
		zap.String("agent", name),
		zap.Uint32("instance_id", instanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	storage, err := attachReady(path, deadline, log)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to shared memory %q: %w", path, err)
	}
	if int(instanceID) >= len(storage.Instances) {
		return nil, errors.Join(
			fmt.Errorf("shared memory %q holds no dataplane instance %d", path, instanceID),
			storage.Close(),
		)
	}
	owner, err := agent.Attach(storage.Instances[instanceID], name, uint64(cfg.MemoryRequirements.Unwrap()))
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to attach agent to shared memory: %w", err),
			storage.Close(),
		)
	}
	return &nativeAttachment{Agent: owner, storage: storage}, nil
}

// attachReady maps the storage at path, retrying while one of its
// instances is not ready yet, until the deadline or, without one, the
// ready bound.
//
// Any other refusal of the storage fails at once.
func attachReady(path string, deadline time.Time, log *zap.Logger) (*zone.Storage, error) {
	if deadline.IsZero() {
		deadline = time.Now().Add(readyTimeout)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	retry := xbackoff.New(
		10*time.Millisecond,
		xbackoff.WithMax(2*time.Second),
		xbackoff.WithOnRetry(func(_ int, _ time.Duration, err error) {
			log.Debug("waiting for dataplane instances", zap.Error(err))
		}),
	)
	var storage *zone.Storage
	var attachErr error
	waitErr := retry.RunContext(ctx, func() error {
		storage, attachErr = zone.Attach(path)
		if errors.Is(attachErr, zone.ErrNotReady) {
			return attachErr
		}
		return nil
	})
	if waitErr != nil {
		return nil, fmt.Errorf("instances not ready by %s: %w", deadline.Format(time.RFC3339), attachErr)
	}
	return storage, attachErr
}

// Close unmaps the storage.
//
// The agent itself needs no detach: C's detach does nothing either.
func (m *nativeAttachment) Close() error {
	return m.storage.Close()
}
