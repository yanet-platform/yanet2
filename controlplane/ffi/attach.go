package ffi

import (
	"errors"
	"fmt"

	"github.com/c2h5oh/datasize"
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// AttachConfig names the dataplane instance an agent serves and the shared
// memory it attaches to.
//
// A module or device control plane embeds it inline in its own config, so
// the keys sit at the top level of that config's document.
type AttachConfig struct {
	// InstanceID specifies which dataplane instance the agent serves.
	//
	// Required: a listed module or device must set it explicitly, even
	// to 0.
	InstanceID xcfg.Required[uint32] `yaml:"instance_id"`
	// MemoryPath is the path to the shared-memory file that is used to
	// communicate with the dataplane.
	MemoryPath xcfg.NonEmptyString `yaml:"memory_path"`
	// MemoryRequirements is the size of the agent's arena in shared
	// memory.
	MemoryRequirements xcfg.NonZero[datasize.ByteSize] `yaml:"memory_requirements"`
}

// DefaultAttachConfig returns the shared-memory defaults with the given
// arena size and no instance, which every listed agent must set itself.
func DefaultAttachConfig(memory datasize.ByteSize) AttachConfig {
	return AttachConfig{
		MemoryPath:         xcfg.MustNonEmptyString("/dev/hugepages/yanet"),
		MemoryRequirements: xcfg.MustNonZero(memory),
	}
}

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
func Attach(cfg AttachConfig, name string, log *zap.Logger) (*Attachment, error) {
	shm, err := AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug("mapping shared memory",
		zap.String("agent", name),
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(name, cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to attach agent to shared memory: %w", err),
			shm.Detach(),
		)
	}

	return &Attachment{Agent: agent, shm: shm}, nil
}

// Close releases the agent and unmaps the shared memory.
func (m *Attachment) Close() error {
	return errors.Join(m.Agent.Close(), m.shm.Detach())
}
