package modulebase

import (
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

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
func attachNative(cfg ffi.AttachConfig, name string, log *zap.Logger) (*nativeAttachment, error) {
	path := cfg.MemoryPath.Unwrap()
	instanceID := cfg.InstanceID.Unwrap()
	log.Debug("mapping shared memory",
		zap.String("agent", name),
		zap.Uint32("instance_id", instanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	storage, err := zone.Attach(path)
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

// Close unmaps the storage.
//
// The agent itself needs no detach: C's detach does nothing either.
func (m *nativeAttachment) Close() error {
	return m.storage.Close()
}
