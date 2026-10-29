package modulebase_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
)

// Test_BaseConfig_InlinesIntoEmbeddingDocuments verifies that a config
// embedding the base keeps the attach and endpoint keys at the top
// level of its own document, beside its module-specific fields.
func Test_BaseConfig_InlinesIntoEmbeddingDocuments(t *testing.T) {
	type config struct {
		modulebase.BaseConfig `yaml:",inline"`

		ModuleField bool `yaml:"module_field"`
	}

	var parsed config
	document := `
instance_id: 3
memory_path: /dev/hugepages/test
memory_requirements: 32MB
endpoint: '[::1]:9'
module_field: true
`
	require.NoError(t, yaml.Unmarshal([]byte(document), &parsed))

	require.EqualValues(t, 3, parsed.InstanceID.Unwrap())
	require.Equal(t, "/dev/hugepages/test", parsed.MemoryPath.Unwrap())
	require.Equal(t, datasize.ByteSize(32*datasize.MB), parsed.MemoryRequirements.Unwrap())
	require.Equal(t, "[::1]:9", parsed.Endpoint.Unwrap())
	require.True(t, parsed.ModuleField)
}

// Test_NewBaseConfig_HoldsSharedDefaults verifies the shared defaults:
// the given arena size and the wildcard loopback endpoint.
func Test_NewBaseConfig_HoldsSharedDefaults(t *testing.T) {
	config := modulebase.NewBaseConfig(4 * datasize.MB)

	require.Equal(t, datasize.ByteSize(4*datasize.MB), config.MemoryRequirements.Unwrap())
	require.Equal(t, "/dev/hugepages/yanet", config.MemoryPath.Unwrap())
	require.Equal(t, "[::1]:0", config.Endpoint.Unwrap())
}
