package ffi_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// moduleConfig mirrors how a module config embeds the attach config: the
// attach keys sit at the document's top level beside the module's own.
type moduleConfig struct {
	ffi.AttachConfig `yaml:",inline"`

	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// Test_AttachConfig_InlineDecode verifies that the attach keys decode from
// the top level of an embedding config and that the embedding config's own
// keys still decode beside them.
func Test_AttachConfig_InlineDecode(t *testing.T) {
	cfg := moduleConfig{AttachConfig: ffi.DefaultAttachConfig(16 * datasize.MB)}
	err := xcfg.Decode([]byte(
		"instance_id: 2\nmemory_path: /dev/hugepages/test\nendpoint: '[::1]:9000'\n",
	), &cfg, xcfg.WithKnownFields())
	require.NoError(t, err)

	require.Equal(t, uint32(2), cfg.InstanceID.Unwrap())
	require.Equal(t, "/dev/hugepages/test", cfg.MemoryPath.Unwrap())
	require.Equal(t, 16*datasize.MB, cfg.MemoryRequirements.Unwrap())
	require.Equal(t, "[::1]:9000", cfg.Endpoint.Unwrap())
}

// Test_AttachConfig_UnknownKeyRejected verifies that an unknown top-level
// key is still rejected when the attach keys are merged in by inlining.
func Test_AttachConfig_UnknownKeyRejected(t *testing.T) {
	cfg := moduleConfig{AttachConfig: ffi.DefaultAttachConfig(16 * datasize.MB)}
	err := xcfg.Decode([]byte("instance_id: 0\nmemory_size: 1\n"), &cfg, xcfg.WithKnownFields())
	require.Error(t, err)
	require.Contains(t, err.Error(), "memory_size")
}

// Test_DefaultAttachConfig_LeavesInstanceUnset verifies that the defaults
// carry the memory path and the given arena size but no instance, so a
// listed agent that omits it still fails validation.
func Test_DefaultAttachConfig_LeavesInstanceUnset(t *testing.T) {
	cfg := ffi.DefaultAttachConfig(64 * datasize.MB)

	require.Equal(t, "/dev/hugepages/yanet", cfg.MemoryPath.Unwrap())
	require.Equal(t, 64*datasize.MB, cfg.MemoryRequirements.Unwrap())
	require.Error(t, cfg.InstanceID.Validate())
}
