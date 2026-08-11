package mirror

import (
	"gopkg.in/yaml.v3"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

type Config struct {
	// InstanceID specifies which dataplane instance this module serves.
	//
	// Required: a listed module must set it explicitly, even to 0.
	InstanceID xcfg.Required[uint32] `yaml:"instance_id"`
	// MemoryPath is the path to the shared-memory file that is used to
	// communicate with dataplane.
	MemoryPath xcfg.NonEmptyString `yaml:"memory_path"`
	// MemoryRequirements is the amount of memory that is required for a single
	// transaction.
	MemoryRequirements xcfg.NonZero[datasize.ByteSize] `yaml:"memory_requirements"`

	Endpoint        xcfg.NonEmptyString `yaml:"endpoint"`
	GatewayEndpoint xcfg.NonEmptyString `yaml:"gateway_endpoint"`
}

func DefaultConfig() *Config {
	return &Config{
		MemoryPath:         xcfg.MustNonEmptyString("/dev/hugepages/yanet"),
		MemoryRequirements: xcfg.MustNonZero(16 * datasize.MB),
		Endpoint:           xcfg.MustNonEmptyString("[::1]:0"),
		GatewayEndpoint:    xcfg.MustNonEmptyString("[::1]:8080"),
	}
}

// UnmarshalYAML seeds Config with DefaultConfig before decoding, so a listed
// module that omits an optional field keeps its default.
//
// The plain alias drops Config's own UnmarshalYAML method so node.Decode
// does not recurse into this method.
func (m *Config) UnmarshalYAML(node *yaml.Node) error {
	*m = *DefaultConfig()
	type plain Config
	return node.Decode((*plain)(m))
}
