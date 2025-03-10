package bird

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type testCfg struct {
	Bird Config
}

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		cfg      string
		expected *Config
	}{
		{
			cfg:      "enable: true",
			expected: nil,
		},
		{
			cfg:      "enable: false",
			expected: &Config{},
		},
		{
			cfg: `
enable: true
sockets:
  - name: master4
    path: /var/lib/export-master4.sock
dump_timeout: 5s
`,
			expected: &Config{
				Enable: true,
				Sockets: []Socket{
					{
						Name: "master4",
						Path: "/var/lib/export-master4.sock",
					},
				},
				DumpTimeout: time.Second * 5,
			},
		},
	}

	for idx, c := range cases {
		t.Run(fmt.Sprintf("case #%d", idx), func(t *testing.T) {
			cfg := &Config{}
			err := yaml.Unmarshal([]byte(c.cfg), cfg)
			if c.expected == nil {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, c.expected, cfg)
			}
		})
	}
}
