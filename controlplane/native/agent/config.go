package agent

import (
	"github.com/c2h5oh/datasize"

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
