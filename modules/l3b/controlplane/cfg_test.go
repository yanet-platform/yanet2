package l3b_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	l3b "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
)

// Test_Config_AttachmentYAML verifies that flat attachment keys retain the
// module's defaults, overrides, and required instance selection.
func Test_Config_AttachmentYAML(t *testing.T) {
	cases := []struct {
		name       string
		document   string
		instance   uint32
		memoryPath string
		memory     datasize.ByteSize
		endpoint   string
		errorPath  string
		lineError  bool
	}{
		{
			name:       "explicit zero instance preserves defaults",
			document:   "instance_id: 0",
			memoryPath: "/dev/hugepages/yanet",
			memory:     64 * datasize.MB,
			endpoint:   "[::1]:0",
		},
		{
			name:       "flat keys override defaults",
			document:   "instance_id: 3\nmemory_path: /tmp/l3b\nmemory_requirements: 128MB\nendpoint: '[::1]:9000'",
			instance:   3,
			memoryPath: "/tmp/l3b",
			memory:     128 * datasize.MB,
			endpoint:   "[::1]:9000",
		},
		{name: "missing instance is rejected", document: "{}", errorPath: "instance_id"},
		{name: "empty memory path is rejected", document: "instance_id: 0\nmemory_path: ''", lineError: true},
		{name: "zero arena is rejected", document: "instance_id: 0\nmemory_requirements: 0", lineError: true},
		{name: "empty endpoint is rejected", document: "instance_id: 0\nendpoint: ''", lineError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := l3b.DefaultConfig()
			err := xcfg.Decode([]byte(tc.document), config)
			if tc.lineError {
				var lineError *xcfg.LineError
				require.ErrorAs(t, err, &lineError)
				return
			}
			if tc.errorPath != "" {
				var pathError *xcfg.PathError
				require.ErrorAs(t, err, &pathError)
				require.Equal(t, tc.errorPath, pathError.Path)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.instance, config.InstanceID.Unwrap())
			require.Equal(t, tc.memoryPath, config.MemoryPath.Unwrap())
			require.Equal(t, tc.memory, config.MemoryRequirements.Unwrap())
			require.Equal(t, tc.endpoint, config.Endpoint.Unwrap())
			config.Default()
			require.Equal(t, l3b.DefaultConfig(), config)
		})
	}
}
