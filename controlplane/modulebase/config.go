package modulebase

import (
	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// BaseConfig is the shared configuration document of a module or device
// control plane: which dataplane instance its agent serves and where
// its gRPC endpoint binds.
//
// A module embeds it inline, so the keys sit at the top level of the
// module's own document, and adds its module-specific fields beside it.
type BaseConfig struct {
	ffi.AttachConfig `yaml:",inline"`

	// Endpoint is the gRPC endpoint address.
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// NewBaseConfig returns the shared defaults for an agent arena of the
// given size.
func NewBaseConfig(memory datasize.ByteSize) BaseConfig {
	return BaseConfig{
		AttachConfig: ffi.DefaultAttachConfig(memory),
		Endpoint:     xcfg.MustNonEmptyString("[::1]:0"),
	}
}
