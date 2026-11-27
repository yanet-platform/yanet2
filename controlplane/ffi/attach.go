package ffi

import (
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
)

// Attachment is an agent attached to a dataplane instance together with
// the shared-memory mapping it lives in.
type Attachment struct {
	Agent *Agent

	shm *SharedMemory
}

// Attach maps the shared memory the config names and attaches the named
// agent to its dataplane instance.
//
// A failed agent attach unmaps the memory again, so an error leaves
// nothing behind for the caller to release.
func Attach(cfg agent.AttachConfig, name string, log *zap.Logger) (*Attachment, error) {
	shm, err := AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug("mapping shared memory",
		zap.String("agent", name),
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	owner, err := shm.AgentAttach(name, cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to attach agent to shared memory: %w", err),
			shm.Detach(),
		)
	}

	return &Attachment{Agent: owner, shm: shm}, nil
}

// Close releases the agent and unmaps the shared memory.
func (m *Attachment) Close() error {
	return errors.Join(m.Agent.Close(), m.shm.Detach())
}
