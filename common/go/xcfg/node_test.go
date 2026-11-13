package xcfg_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

type nodeHeaders map[string]string

func (m *nodeHeaders) UnmarshalText(data []byte) error {
	var headers map[string]string
	if err := json.Unmarshal(data, &headers); err != nil {
		return err
	}
	*m = headers
	return nil
}

type nodeSettings struct {
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	Headers  nodeHeaders         `yaml:"headers"`
}

type nodeSource struct {
	Type   xcfg.NonEmptyString `yaml:"type"`
	Config xcfg.Node           `yaml:"config"`
}

func (m *nodeSource) Validate() error {
	var settings nodeSettings
	return m.Config.Decode(&settings)
}

// Test_Node_DeferredEnvironment verifies that a factory can decode required
// settings during validation and later use the same captured environment.
func Test_Node_DeferredEnvironment(t *testing.T) {
	type Config struct {
		Sources []nodeSource `yaml:"sources"`
	}
	input := []byte(`
sources:
  - type: http
    config:
      headers:
        X-Removed: file-value
  - type: http
    config:
      endpoint: second
      headers:
        Authorization: file-value
`)
	t.Setenv("CUSTOM_SOURCES_0_CONFIG_ENDPOINT", "first")
	t.Setenv("CUSTOM_SOURCES_0_CONFIG_HEADERS", `{"Authorization":"env-value"}`)
	t.Setenv("CUSTOM_SOURCES_1_CONFIG_HEADERS", `{}`)
	var config Config
	require.NoError(t, xcfg.Decode(input, &config, xcfg.WithEnvPrefix("CUSTOM_")))

	t.Setenv("CUSTOM_SOURCES_0_CONFIG_ENDPOINT", "")
	t.Setenv("CUSTOM_SOURCES_0_CONFIG_HEADERS", `{}`)
	var first, second nodeSettings
	require.NoError(t, config.Sources[0].Config.Decode(&first))
	require.NoError(t, config.Sources[1].Config.Decode(&second))
	require.Equal(t, "first", first.Endpoint.Unwrap())
	require.Equal(t, nodeHeaders{"Authorization": "env-value"}, first.Headers)
	require.Equal(t, "second", second.Endpoint.Unwrap())
	require.Empty(t, second.Headers)

	encoded, err := yaml.Marshal(&config.Sources[0].Config)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "env-value")
	require.Contains(t, string(encoded), "file-value")

	require.NoError(t, config.Sources[0].Config.Encode(nodeSettings{
		Endpoint: xcfg.MustNonEmptyString("from-go"),
	}))
	var supplied nodeSettings
	require.NoError(t, config.Sources[0].Config.Decode(&supplied))
	require.Equal(t, "from-go", supplied.Endpoint.Unwrap())
	require.Empty(t, supplied.Headers)
}

// Test_Node_ValidationOptions verifies that deferred settings keep the
// loader's environment and unknown-field validation rules.
func Test_Node_ValidationOptions(t *testing.T) {
	input := []byte("type: http\nconfig:\n  endpoint: from-file\n  unknown: value\n")
	t.Setenv("YANET_CONFIG_ENDPOINT", "")

	var source nodeSource
	require.NoError(t, xcfg.Decode(input, &source))
	require.Error(t, xcfg.Decode(input, &source, xcfg.WithEnv()))
	require.ErrorContains(t,
		xcfg.Decode(input, &source, xcfg.WithKnownFields()),
		`unknown key "unknown"`,
	)
}
