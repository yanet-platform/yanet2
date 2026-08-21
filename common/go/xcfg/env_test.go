package xcfg_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"gopkg.in/yaml.v3"
)

// envOverrides puts env in the process environment for the duration of the
// test and enables the overlay, so a test states the variables it relies on
// and nothing else.
func envOverrides(t *testing.T, env map[string]string) xcfg.Option {
	t.Helper()
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if !found || !strings.HasPrefix(name, xcfg.DefaultEnvPrefix) {
			continue
		}
		require.NoError(t, os.Unsetenv(name))
		t.Cleanup(func() {
			require.NoError(t, os.Setenv(name, value))
		})
	}
	for name, value := range env {
		t.Setenv(name, value)
	}

	return xcfg.WithEnv()
}

func Test_Env_OverridesScalar(t *testing.T) {
	type Config struct {
		Name xcfg.NonEmptyString `yaml:"name"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_NAME": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Name.Unwrap())
}

func Test_Env_OverridesNestedField(t *testing.T) {
	type Server struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Server Server `yaml:"server"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("server:\n  endpoint: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_ENDPOINT": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Server.Endpoint.Unwrap())
}

// A key that itself contains an underscore must not be split at the wrong
// place: the name is generated from the type, never parsed out of the
// environment.
func Test_Env_KeyWithUnderscoreNotSplit(t *testing.T) {
	type Server struct {
		HTTPEndpoint xcfg.NonEmptyString `yaml:"http_endpoint"`
	}
	type Config struct {
		Server Server `yaml:"server"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("server:\n  http_endpoint: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_HTTP_ENDPOINT": ":8080"}),
	))
	require.Equal(t, ":8080", cfg.Server.HTTPEndpoint.Unwrap())
}

// An endpoint is the most common value in this config and would resolve as a
// YAML flow sequence if it were emitted untagged.
func Test_Env_EndpointNotParsedAsSequence(t *testing.T) {
	type Config struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("endpoint: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_ENDPOINT": "[::1]:8080"}),
	))
	require.Equal(t, "[::1]:8080", cfg.Endpoint.Unwrap())
}

func Test_Env_EndpointIntoPlainString(t *testing.T) {
	type Config struct {
		Endpoint string `yaml:"endpoint"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("endpoint: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_ENDPOINT": "[::1]:0"}),
	))
	require.Equal(t, "[::1]:0", cfg.Endpoint)
}

// A value that arrives from the environment goes through the destination
// type's own decoding, so it is validated exactly like one written in the
// file.
func Test_Env_RejectsEmptyNonEmptyString(t *testing.T) {
	type Config struct {
		Name xcfg.NonEmptyString `yaml:"name"`
	}

	var cfg Config
	require.Error(t, xcfg.Decode(
		[]byte("name: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_NAME": ""}),
	))
}

func Test_Env_CreatesAbsentKey(t *testing.T) {
	type Config struct {
		Name xcfg.NonEmptyString `yaml:"name"`
		Path xcfg.NonEmptyString `yaml:"path"`
	}

	cfg := Config{Path: xcfg.MustNonEmptyString("/default")}
	require.NoError(t, xcfg.Decode(
		[]byte("{}"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_NAME": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Name.Unwrap())
	require.Equal(t, "/default", cfg.Path.Unwrap())
}

func Test_Env_EmptyDocument(t *testing.T) {
	type Config struct {
		Name xcfg.NonEmptyString `yaml:"name"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		nil,
		&cfg,
		envOverrides(t, map[string]string{"YANET_NAME": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Name.Unwrap())
}

func Test_Env_PreservesDefaultsWhenNoVariable(t *testing.T) {
	type Config struct {
		Name xcfg.NonEmptyString `yaml:"name"`
		Path xcfg.NonEmptyString `yaml:"path"`
	}

	cfg := Config{Path: xcfg.MustNonEmptyString("/default")}
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file"),
		&cfg,
		envOverrides(t, map[string]string{}),
	))
	require.Equal(t, "from-file", cfg.Name.Unwrap())
	require.Equal(t, "/default", cfg.Path.Unwrap())
}

func Test_Env_UnrelatedVariableIgnored(t *testing.T) {
	type Config struct {
		Name xcfg.NonEmptyString `yaml:"name"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_NOT_A_FIELD": "x"}),
	))
	require.Equal(t, "from-file", cfg.Name.Unwrap())
}

// Case-specific environment setup is isolated from matching variables already
// present in the test process.
func Test_Env_InheritedVariableCleared(t *testing.T) {
	type Config struct {
		Name string `yaml:"name"`
		Path string `yaml:"path"`
	}

	t.Setenv("YANET_PATH", "inherited")
	cfg := Config{Path: "default"}
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_NAME": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Name)
	require.Equal(t, "default", cfg.Path)
}

// Leaving the document untouched when nothing applies is what keeps reported
// error lines pointing at the user's own file.
func Test_Env_ErrorLineFollowsFileWithoutMatch(t *testing.T) {
	type Config struct {
		Name    string `yaml:"name"`
		Workers int    `yaml:"workers"`
	}

	var cfg Config
	err := xcfg.Decode(
		[]byte("# a comment\n\nname: foo\nworkers: not-a-number\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_NOT_A_FIELD": "x"}),
	)
	require.ErrorContains(t, err, "line 4")
}

func Test_Env_OverridesNumeric(t *testing.T) {
	type Config struct {
		Workers xcfg.NonZero[uint16] `yaml:"workers"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("workers: 1"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_WORKERS": "4"}),
	))
	require.Equal(t, uint16(4), cfg.Workers.Unwrap())
}

func Test_Env_RejectsZeroNonZero(t *testing.T) {
	type Config struct {
		Workers xcfg.NonZero[uint16] `yaml:"workers"`
	}

	var cfg Config
	require.Error(t, xcfg.Decode(
		[]byte("workers: 1"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_WORKERS": "0"}),
	))
}

func Test_Env_OverridesBool(t *testing.T) {
	type Config struct {
		Debug bool `yaml:"debug"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("debug: false"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_DEBUG": "true"}),
	))
	require.True(t, cfg.Debug)
}

func Test_Env_OverridesDuration(t *testing.T) {
	type Config struct {
		Timeout time.Duration `yaml:"timeout"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("timeout: 1s"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_TIMEOUT": "1500ms"}),
	))
	require.Equal(t, 1500*time.Millisecond, cfg.Timeout)
}

func Test_Env_MaterializesAbsentOptional(t *testing.T) {
	type Inner struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Server xcfg.Optional[Inner] `yaml:"server"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("{}"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_ENDPOINT": "from-env"}),
	))
	require.NotNil(t, cfg.Server.Unwrap())
	require.Equal(t, "from-env", cfg.Server.Unwrap().Endpoint.Unwrap())
}

func Test_Env_LeavesOptionalAbsent(t *testing.T) {
	type Inner struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Name   xcfg.NonEmptyString  `yaml:"name"`
		Server xcfg.Optional[Inner] `yaml:"server"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_NAME": "from-env"}),
	))
	require.Nil(t, cfg.Server.Unwrap())
}

func Test_Env_SetsRequired(t *testing.T) {
	type Config struct {
		Name xcfg.Required[string] `yaml:"name"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("{}"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_NAME": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Name.Unwrap())
}

func Test_Env_OverridesSequenceElement(t *testing.T) {
	type Gateway struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Gateways []Gateway `yaml:"gateways"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("gateways:\n  - endpoint: a\n  - endpoint: b\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_1_ENDPOINT": "[::1]:8080"}),
	))
	require.Len(t, cfg.Gateways, 2)
	require.Equal(t, "a", cfg.Gateways[0].Endpoint.Unwrap())
	require.Equal(t, "[::1]:8080", cfg.Gateways[1].Endpoint.Unwrap())
}

func Test_Env_AppendsSequenceElement(t *testing.T) {
	type Gateway struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Gateways []Gateway `yaml:"gateways"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("gateways:\n  - endpoint: a\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_1_ENDPOINT": "b"}),
	))
	require.Len(t, cfg.Gateways, 2)
	require.Equal(t, "a", cfg.Gateways[0].Endpoint.Unwrap())
	require.Equal(t, "b", cfg.Gateways[1].Endpoint.Unwrap())
}

func Test_Env_SequenceGapRejected(t *testing.T) {
	type Gateway struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Gateways []Gateway `yaml:"gateways"`
	}

	var cfg Config
	err := xcfg.Decode(
		[]byte("gateways: []\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_2_ENDPOINT": "c"}),
	)
	require.ErrorContains(t, err, "YANET_GATEWAYS_0")
}

func Test_Env_SequenceOfScalars(t *testing.T) {
	type Config struct {
		Names []string `yaml:"names"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("names:\n  - a\n"),
		&cfg,
		envOverrides(t, map[string]string{
			"YANET_NAMES_0": "x",
			"YANET_NAMES_1": "y",
		}),
	))
	require.Equal(t, []string{"x", "y"}, cfg.Names)
}

// Two config paths folding onto one variable would make the override
// silently ambiguous.
func Test_Env_CollisionRejected(t *testing.T) {
	type HTTP struct {
		Endpoint string `yaml:"endpoint"`
	}
	type Config struct {
		HTTPEndpoint string `yaml:"http_endpoint"`
		HTTP         HTTP   `yaml:"http"`
	}

	var cfg Config
	err := xcfg.Decode(
		[]byte("{}"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_HTTP_ENDPOINT": "x"}),
	)
	require.ErrorContains(t, err, "maps to both")
}

func Test_Env_InlineStruct(t *testing.T) {
	type Common struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Common `yaml:",inline"`
		Name   xcfg.NonEmptyString `yaml:"name"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: n\nendpoint: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_ENDPOINT": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Endpoint.Unwrap())
	require.Equal(t, "n", cfg.Name.Unwrap())
}

func Test_Env_SkipsIgnoredField(t *testing.T) {
	type Config struct {
		Name   string `yaml:"name"`
		Hidden string `yaml:"-"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_HIDDEN": "x"}),
	))
	require.Empty(t, cfg.Hidden)
}

// A field with no explicit tag name is keyed by its lowercased Go name,
// matching yaml.v3's own default key resolution.
func Test_Env_UntaggedFieldUsesLowercasedName(t *testing.T) {
	type Config struct {
		Endpoint string
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("endpoint: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_ENDPOINT": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Endpoint)
}

// A map has no fixed key set to generate names from, so its entries stay
// file-only rather than being silently half-addressable.
func Test_Env_MapEntriesStayFileOnly(t *testing.T) {
	type Config struct {
		Labels map[string]string `yaml:"labels"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("labels:\n  a: from-file\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_LABELS_A": "from-env"}),
	))
	require.Equal(t, map[string]string{"a": "from-file"}, cfg.Labels)
}

// A sibling key that is a prefix of another sibling key must not be
// descended into: YANET_GATEWAY_DEVICES_A belongs to gateway_devices, yet it
// opens with every character of the name gateway generates. Materialising
// gateway as an empty mapping would decode as null and wipe its defaults.
func Test_Env_PrefixSiblingKeyNotDescended(t *testing.T) {
	type Gateway struct {
		Endpoint string `yaml:"endpoint"`
	}
	type Config struct {
		Gateway        Gateway           `yaml:"gateway"`
		GatewayDevices map[string]string `yaml:"gateway_devices"`
	}

	cfg := Config{Gateway: Gateway{Endpoint: "default"}}
	require.NoError(t, xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAY_DEVICES_A": "x"}),
	))
	require.Equal(t, "default", cfg.Gateway.Endpoint)
}

// Skipping a prefix sibling must not swallow a genuine collision: when the
// longer key's own name is also the name a nested leaf generates, the two
// really are ambiguous and the override stays rejected.
func Test_Env_PrefixSiblingStillCollides(t *testing.T) {
	type Table struct {
		Name string `yaml:"name"`
	}
	type Config struct {
		Table     Table  `yaml:"table"`
		TableName string `yaml:"table_name"`
	}

	var cfg Config
	err := xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_TABLE_NAME": "from-env"}),
	)
	require.ErrorContains(t, err, "maps to both")
}

// A variable that names nothing in the type must leave the document alone
// rather than seed an empty mapping the file never had.
func Test_Env_StrayVariableLeavesDefaults(t *testing.T) {
	type Server struct {
		Endpoint string `yaml:"endpoint"`
	}
	type Config struct {
		Server Server `yaml:"server"`
	}

	cfg := Config{Server: Server{Endpoint: "default"}}
	require.NoError(t, xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_BOGUS": "x"}),
	))
	require.Equal(t, "default", cfg.Server.Endpoint)
}

// A sequence index is honoured only when a variable names a leaf inside that
// element; a name that merely looks like an index must not append an element
// no leaf ever fills.
func Test_Env_StrayIndexNotAppended(t *testing.T) {
	type Gateway struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Gateways []Gateway `yaml:"gateways"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("gateways:\n  - endpoint: a\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_1_BOGUS": "x"}),
	))
	require.Len(t, cfg.Gateways, 1)
	require.Equal(t, "a", cfg.Gateways[0].Endpoint.Unwrap())
}

func Test_Env_OverridesThroughPointer(t *testing.T) {
	type Inner struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Server *Inner `yaml:"server"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("server:\n  endpoint: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_ENDPOINT": "from-env"}),
	))
	require.NotNil(t, cfg.Server)
	require.Equal(t, "from-env", cfg.Server.Endpoint.Unwrap())
}

// Overriding through an alias must not leak into every other user of the
// same anchor.
func Test_Env_AliasNotLeaked(t *testing.T) {
	type Inner struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		A Inner `yaml:"a"`
		B Inner `yaml:"b"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("a: &shared\n  endpoint: shared\nb: *shared\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_B_ENDPOINT": "from-env"}),
	))
	require.Equal(t, "shared", cfg.A.Endpoint.Unwrap())
	require.Equal(t, "from-env", cfg.B.Endpoint.Unwrap())
}

// Detaching an aliased container keeps aliases inside the copied subtree
// connected to copied anchors rather than the original subtree.
func Test_Env_AliasCopyKeepsInternalAliases(t *testing.T) {
	type Inner struct {
		Endpoint string `yaml:"endpoint"`
		Mirror   string `yaml:"mirror"`
	}
	type Config struct {
		Dummy   string `yaml:"dummy"`
		A       Inner  `yaml:"a"`
		B       Inner  `yaml:"b"`
		Outside string `yaml:"outside"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("dummy: &endpoint_env_copy_1 external\n"+
			"a: &shared\n  endpoint: &endpoint old\n  mirror: *endpoint\n"+
			"b: *shared\noutside: *endpoint_env_copy_1\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_B_ENDPOINT": "from-env"}),
	))
	require.Equal(t, "external", cfg.Dummy)
	require.Equal(t, Inner{Endpoint: "old", Mirror: "old"}, cfg.A)
	require.Equal(t, Inner{Endpoint: "from-env", Mirror: "from-env"}, cfg.B)
	require.Equal(t, "external", cfg.Outside)
}

// Overriding a scalar that defines an anchor must keep the anchor, or every
// alias to it is left dangling. The packaged control plane config does this
// with memory_path.
func Test_Env_AnchoredScalarKeepsAliases(t *testing.T) {
	type Module struct {
		MemoryPath string `yaml:"memory_path"`
	}
	type Config struct {
		MemoryPath string   `yaml:"memory_path"`
		Modules    []Module `yaml:"modules"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("memory_path: &memory_path /dev/hugepages/yanet\n"+
			"modules:\n"+
			"  - memory_path: *memory_path\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_MEMORY_PATH": "/dev/hugepages/other"}),
	))
	require.Equal(t, "/dev/hugepages/other", cfg.MemoryPath)
	require.Len(t, cfg.Modules, 1)
	require.Equal(t, "/dev/hugepages/other", cfg.Modules[0].MemoryPath)
}

// Materialising defaults into an anchored null container must keep aliases to
// that container valid and pointed at the overlaid value.
func Test_Env_MaterializedContainerKeepsAliases(t *testing.T) {
	type Gateway struct {
		Endpoint string `yaml:"endpoint"`
	}
	type Config struct {
		Gateways []Gateway `yaml:"gateways"`
		Mirror   []Gateway `yaml:"mirror"`
	}

	cfg := Config{Gateways: []Gateway{{Endpoint: "from-default"}}}
	require.NoError(t, xcfg.Decode(
		[]byte("gateways: &gateways\nmirror: *gateways\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_0_ENDPOINT": "from-env"}),
	))
	require.Equal(t, []Gateway{{Endpoint: "from-env"}}, cfg.Gateways)
	require.Equal(t, []Gateway{{Endpoint: "from-env"}}, cfg.Mirror)
}

// A key held only through a "<<" merge must keep the rest of what it
// inherits when one of its children is overridden.
func Test_Env_MergedKeyKeepsInheritedFields(t *testing.T) {
	type Server struct {
		Endpoint string `yaml:"endpoint"`
		Timeout  string `yaml:"timeout"`
	}
	type Config struct {
		Server Server `yaml:"server"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("defaults: &defaults\n"+
			"  server:\n"+
			"    endpoint: from-file\n"+
			"    timeout: 5s\n"+
			"<<: *defaults\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_ENDPOINT": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Server.Endpoint)
	require.Equal(t, "5s", cfg.Server.Timeout)
}

// An override lands under the same scrutiny as a key written in the file.
func Test_Env_OverrideIsKnownKey(t *testing.T) {
	type Config struct {
		Name string `yaml:"name"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file"),
		&cfg,
		xcfg.WithKnownFields(),
		envOverrides(t, map[string]string{"YANET_NAME": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Name)
}

func Test_Env_KnownFieldsStillRejectsUnknownKey(t *testing.T) {
	type Config struct {
		Name string `yaml:"name"`
	}

	var cfg Config
	require.Error(t, xcfg.Decode(
		[]byte("name: from-file\nnope: x\n"),
		&cfg,
		xcfg.WithKnownFields(),
		envOverrides(t, map[string]string{"YANET_NAME": "from-env"}),
	))
}

// Only the prefixed namespace is consulted.
func Test_Env_ReadsProcessEnvironment(t *testing.T) {
	type Config struct {
		Name xcfg.NonEmptyString `yaml:"name"`
	}

	t.Setenv("YANET_NAME", "from-env")
	t.Setenv("UNRELATED_NAME", "ignored")

	var cfg Config
	require.NoError(t, xcfg.Decode([]byte("name: from-file"), &cfg, xcfg.WithEnv()))
	require.Equal(t, "from-env", cfg.Name.Unwrap())
}

func Test_Env_DisabledWithoutOption(t *testing.T) {
	type Config struct {
		Name xcfg.NonEmptyString `yaml:"name"`
	}

	t.Setenv("YANET_NAME", "from-env")

	var cfg Config
	require.NoError(t, xcfg.Decode([]byte("name: from-file"), &cfg))
	require.Equal(t, "from-file", cfg.Name.Unwrap())
}

func Test_Env_CustomPrefix(t *testing.T) {
	type Config struct {
		Name xcfg.NonEmptyString `yaml:"name"`
	}

	t.Setenv("YANET_NAME", "shared-namespace")
	t.Setenv("ANNOUNCER_NAME", "from-env")

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file"),
		&cfg,
		xcfg.WithEnvPrefix("ANNOUNCER_"),
	))
	require.Equal(t, "from-env", cfg.Name.Unwrap())
}

// An underscore-only prefix remains a literal prefix rather than selecting
// the unprefixed namespace.
func Test_Env_UnderscorePrefix(t *testing.T) {
	type Config struct {
		Name string `yaml:"name"`
	}

	t.Setenv("_NAME", "from-env")
	t.Setenv("NAME", "unprefixed")

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file"),
		&cfg,
		xcfg.WithEnvPrefix("_"),
	))
	require.Equal(t, "from-env", cfg.Name)
}

// Letters outside ASCII remain letters in environment names and are
// upper-cased instead of being folded into the separator character.
func Test_Env_UnicodeKeyUppercased(t *testing.T) {
	type Config struct {
		City string `yaml:"münchen"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("münchen: from-file"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_MÜNCHEN": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.City)
}

func Test_Env_LoadConfigAppliesOverride(t *testing.T) {
	type Config struct {
		Name xcfg.NonEmptyString `yaml:"name"`
	}

	path := t.TempDir() + "/config.yaml"
	require.NoError(t, os.WriteFile(path, []byte("name: from-file"), 0o600))

	t.Setenv("YANET_NAME", "from-env")

	cfg, err := xcfg.LoadConfig[Config](path, xcfg.WithEnv())
	require.NoError(t, err)
	require.Equal(t, "from-env", cfg.Name.Unwrap())
}

func Test_Env_RejectsMalformedDocument(t *testing.T) {
	type Config struct {
		Name string `yaml:"name"`
	}

	var cfg Config
	require.Error(t, xcfg.Decode(
		[]byte("name: [unclosed\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_NAME": "from-env"}),
	))
}

// Enabling the environment overlay must not turn an invalid nil destination
// into a reflection panic.
func Test_Env_NilDestinationRejected(t *testing.T) {
	err := xcfg.Decode(
		[]byte("{}\n"),
		nil,
		envOverrides(t, map[string]string{"YANET_NAME": "from-env"}),
	)
	require.Error(t, err)
}

// A nil destination is rejected even when no matching process variable makes
// the overlay walk the destination type.
func Test_Env_NilDestinationRejectedWithoutVariables(t *testing.T) {
	err := xcfg.Decode([]byte("{}\n"), nil, envOverrides(t, map[string]string{}))
	require.Error(t, err)
}

// A self-referential type must not send the name walk into infinite
// recursion. The variable names a path deeper than the walk will follow, so
// the walk gives up and says where.
func Test_Env_BoundsRecursion(t *testing.T) {
	type Node struct {
		Name string `yaml:"name"`
		Next *Node  `yaml:"next"`
	}

	deep := "YANET" + strings.Repeat("_NEXT", 34)

	var cfg Node
	err := xcfg.Decode(
		[]byte("name: root"),
		&cfg,
		envOverrides(t, map[string]string{deep + "_NAME": "x"}),
	)
	require.ErrorContains(t, err, "nests deeper than")
}

// An exact leaf match one level past the bound must reach the applying walk
// and report that bound rather than being mistaken for an unrelated variable.
func Test_Env_ExactLeafPastDepthLimitRejected(t *testing.T) {
	type Node struct {
		Name string `yaml:"name"`
		Next *Node  `yaml:"next"`
	}

	deep := "YANET" + strings.Repeat("_NEXT", 32)

	var cfg Node
	err := xcfg.Decode(
		[]byte("name: root"),
		&cfg,
		envOverrides(t, map[string]string{deep + "_NAME": "x"}),
	)
	require.ErrorContains(t, err, "nests deeper than")
}

// A variable that names nothing must not push a self-referential type into
// that bound: the type could go on forever, but no variable asks it to.
func Test_Env_StrayVariableOnRecursiveType(t *testing.T) {
	type Node struct {
		Name string `yaml:"name"`
		Next *Node  `yaml:"next"`
	}

	var cfg Node
	require.NoError(t, xcfg.Decode(
		[]byte("name: root"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_UNUSED": "x"}),
	))
	require.Equal(t, "root", cfg.Name)
	require.Nil(t, cfg.Next)
}

// A string field must receive the value verbatim, including the values YAML
// would otherwise resolve to a bool, a number or null. The emitter quotes
// some forms on its own, so these are the ones that actually depend on the
// destination type being consulted.
func Test_Env_StringKeepsYAMLHostileValues(t *testing.T) {
	type Config struct {
		V string `yaml:"v"`
	}

	for _, value := range []string{
		"null", "~", "true", "false", "yes", "no", "on", "off",
		"0x10", "007", "1e5", ".inf", ".nan", "12:30", "-", "",
	} {
		t.Run(value, func(t *testing.T) {
			var cfg Config
			require.NoError(t, xcfg.Decode(
				[]byte("v: placeholder"),
				&cfg,
				envOverrides(t, map[string]string{"YANET_V": value}),
			))
			require.Equal(t, value, cfg.V)
		})
	}
}

// The same values must survive the wrapper, which reports the type it
// configures through EnvType.
func Test_Env_NonEmptyStringKeepsYAMLHostileValues(t *testing.T) {
	type Config struct {
		V xcfg.NonEmptyString `yaml:"v"`
	}

	for _, value := range []string{"null", "true", "yes", "0x10", "12:30", "[::1]:8080"} {
		t.Run(value, func(t *testing.T) {
			var cfg Config
			require.NoError(t, xcfg.Decode(
				[]byte("v: placeholder"),
				&cfg,
				envOverrides(t, map[string]string{"YANET_V": value}),
			))
			require.Equal(t, value, cfg.V.Unwrap())
		})
	}
}

// A numeric field must not be handed a quoted scalar, which it could not
// decode.
func Test_Env_NumericNotQuoted(t *testing.T) {
	type Config struct {
		Workers int `yaml:"workers"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("workers: 1"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_WORKERS": "4"}),
	))
	require.Equal(t, 4, cfg.Workers)
}

// An override that the destination type cannot decode must be reported
// rather than silently ignored.
func Test_Env_RejectsUndecodableValue(t *testing.T) {
	type Config struct {
		Workers int `yaml:"workers"`
	}

	var cfg Config
	require.Error(t, xcfg.Decode(
		[]byte("workers: 1"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_WORKERS": "not-a-number"}),
	))
}

// branchingNode is self-referential through two fields, so a walk that
// enumerates its fields before deciding a variable is unrelated costs
// 2^depth calls rather than the linear cost a single link would hide.
type branchingNode struct {
	Left  *branchingNode `yaml:"left"`
	Right *branchingNode `yaml:"right"`
	Name  string         `yaml:"name"`
}

type exportedScalar struct {
	Value int
}

func (m *exportedScalar) UnmarshalYAML(node *yaml.Node) error {
	return node.Decode(&m.Value)
}

type exportedMapping struct {
	Name string `yaml:"name"`
}

func (m *exportedMapping) UnmarshalYAML(node *yaml.Node) error {
	type plain exportedMapping
	return node.Decode((*plain)(m))
}

// A custom scalar decoder owns its YAML representation even when its Go type
// has exported implementation fields.
func Test_Env_CustomYAMLScalarWithExportedFieldOverridden(t *testing.T) {
	type Config struct {
		Port exportedScalar `yaml:"port"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("port: 1\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_PORT": "2"}),
	))
	require.Equal(t, 2, cfg.Port.Value)
}

// A mapping-backed custom decoder retains nested field overrides when no
// exact parent variable is supplied.
func Test_Env_CustomYAMLMappingWithExportedFieldOverridden(t *testing.T) {
	type Config struct {
		Function exportedMapping `yaml:"function"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("function:\n  name: from-file\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_FUNCTION_NAME": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Function.Name)
}

// A variable that names nothing must be answered without expanding a
// branching self-referential type. The assertion is the test terminating:
// before the prune this ran past any timeout.
func Test_Env_StrayVariableOnBranchingRecursiveType(t *testing.T) {
	type Config struct {
		Root branchingNode `yaml:"root"`
		Name string        `yaml:"name"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file\nroot:\n  name: root\n"),
		&cfg,
		envOverrides(t, map[string]string{
			"YANET_NAME":   "from-env",
			"YANET_UNUSED": "x",
		}),
	))
	require.Equal(t, "from-env", cfg.Name)
	require.Equal(t, "root", cfg.Root.Name)
	require.Nil(t, cfg.Root.Left)
	require.Nil(t, cfg.Root.Right)
}

// A parent the file fills with a scalar cannot hold the key an override
// names. Rewriting it would let the override hide a malformed file, so the
// document is left for the decoder to reject.
func Test_Env_MalformedMappingParentStillRejected(t *testing.T) {
	type Server struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Server Server `yaml:"server"`
	}

	var cfg Config
	err := xcfg.Decode(
		[]byte("server: broken\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_ENDPOINT": "from-env"}),
	)
	require.ErrorContains(t, err, "cannot unmarshal")
}

// The same holds for a sequence destination the file fills with a scalar.
func Test_Env_MalformedSequenceParentStillRejected(t *testing.T) {
	type Gateway struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Gateways []Gateway `yaml:"gateways"`
	}

	var cfg Config
	err := xcfg.Decode(
		[]byte("gateways: broken\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_0_ENDPOINT": "from-env"}),
	)
	require.ErrorContains(t, err, "cannot unmarshal")
}

// An explicit null is a key written with no value, which an override is
// still allowed to fill.
func Test_Env_FillsExplicitNullParent(t *testing.T) {
	type Server struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Server Server `yaml:"server"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("server:\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_ENDPOINT": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Server.Endpoint.Unwrap())
}

// A key written as an alias resolves to the same key an override names, so
// the existing entry must be found rather than a second one appended: the
// duplicate would decode as the whole mapping and drop its other fields.
func Test_Env_AliasedMappingKeyResolved(t *testing.T) {
	type Server struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
		Timeout  xcfg.NonEmptyString `yaml:"timeout"`
	}
	type Config struct {
		Key    xcfg.NonEmptyString `yaml:"key"`
		Server Server              `yaml:"server"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("key: &key server\n*key: {endpoint: old, timeout: 5s}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_ENDPOINT": "from-env"}),
	))
	require.Equal(t, "from-env", cfg.Server.Endpoint.Unwrap())
	require.Equal(t, "5s", cfg.Server.Timeout.Unwrap())
}

// An index a fixed array has no element for names nothing in the
// destination type, so it is ignored rather than appended: growing the
// sequence would make yaml reject an otherwise valid file on its length.
func Test_Env_ArrayIndexPastEndIgnored(t *testing.T) {
	type Gateway struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Gateways [2]Gateway `yaml:"gateways"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("gateways:\n  - endpoint: a\n  - endpoint: b\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_2_ENDPOINT": "from-env"}),
	))
	require.Equal(t, "a", cfg.Gateways[0].Endpoint.Unwrap())
	require.Equal(t, "b", cfg.Gateways[1].Endpoint.Unwrap())
}

// An index the array does have is still overridden.
func Test_Env_ArrayIndexInRangeOverridden(t *testing.T) {
	type Gateway struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Gateways [2]Gateway `yaml:"gateways"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("gateways:\n  - endpoint: a\n  - endpoint: b\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_1_ENDPOINT": "from-env"}),
	))
	require.Equal(t, "a", cfg.Gateways[0].Endpoint.Unwrap())
	require.Equal(t, "from-env", cfg.Gateways[1].Endpoint.Unwrap())
}

// An out-of-range array index names nothing in the type, so it must not
// materialise the parent an unrelated override causes to be written out.
// Materialising it would decode as null and wipe the parent's own defaults —
// here, leave a required field unset.
func Test_Env_ArrayIndexPastEndKeepsParentAbsent(t *testing.T) {
	type Gateway struct {
		Endpoint string `yaml:"endpoint"`
	}
	type Server struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
		Gateways [2]Gateway          `yaml:"gateways"`
	}
	type Config struct {
		Name   string  `yaml:"name"`
		Server *Server `yaml:"server"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file\n"),
		&cfg,
		envOverrides(t, map[string]string{
			"YANET_NAME":                       "from-env",
			"YANET_SERVER_GATEWAYS_2_ENDPOINT": "stray",
		}),
	))
	require.Equal(t, "from-env", cfg.Name)
	require.Nil(t, cfg.Server)
}

// An empty prefix asks for the unprefixed namespace, where a top-level key is
// named NAME rather than _NAME.
func Test_Env_EmptyPrefix(t *testing.T) {
	type Server struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Name   string `yaml:"name"`
		Server Server `yaml:"server"`
	}

	t.Setenv("NAME", "from-env")
	t.Setenv("SERVER_ENDPOINT", "endpoint-from-env")

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file\nserver:\n  endpoint: endpoint-from-file\n"),
		&cfg,
		xcfg.WithEnvPrefix(""),
	))
	require.Equal(t, "from-env", cfg.Name)
	require.Equal(t, "endpoint-from-env", cfg.Server.Endpoint.Unwrap())
}

// yaml.Node stores arbitrary raw YAML. Its exported implementation fields are
// not schema fields and must remain file-only.
func Test_Env_YAMLNodeStaysFileOnly(t *testing.T) {
	type Config struct {
		Name string    `yaml:"name"`
		Raw  yaml.Node `yaml:"raw"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("name: from-file\nraw:\n  original: kept\n"),
		&cfg,
		envOverrides(t, map[string]string{
			"YANET_NAME":      "from-env",
			"YANET_RAW_VALUE": "injected",
		}),
	))
	require.Equal(t, "from-env", cfg.Name)
	require.Equal(t, yaml.MappingNode, cfg.Raw.Kind)
	require.Len(t, cfg.Raw.Content, 2)
	require.Equal(t, "original", cfg.Raw.Content[0].Value)
	require.Equal(t, "kept", cfg.Raw.Content[1].Value)
}

// An absent fixed array starts from its destination value so a partial
// override retains every untouched element and still has its declared length.
func Test_Env_AbsentArrayPreservesDefaults(t *testing.T) {
	type Gateway struct {
		Endpoint string `yaml:"endpoint"`
	}
	type Config struct {
		Gateways [2]Gateway `yaml:"gateways"`
	}

	cfg := Config{Gateways: [2]Gateway{{Endpoint: "first"}, {Endpoint: "second"}}}
	require.NoError(t, xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_1_ENDPOINT": "from-env"}),
	))
	require.Equal(t, [2]Gateway{{Endpoint: "first"}, {Endpoint: "from-env"}}, cfg.Gateways)
}

// Supplying every element gives an absent fixed array its exact length and is
// therefore still supported.
func Test_Env_AbsentArrayFullyOverridden(t *testing.T) {
	type Gateway struct {
		Endpoint string `yaml:"endpoint"`
	}
	type Config struct {
		Gateways [2]Gateway `yaml:"gateways"`
	}

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{
			"YANET_GATEWAYS_0_ENDPOINT": "a",
			"YANET_GATEWAYS_1_ENDPOINT": "b",
		}),
	))
	require.Equal(t, "a", cfg.Gateways[0].Endpoint)
	require.Equal(t, "b", cfg.Gateways[1].Endpoint)
}

// An override into an absent defaulted slice starts from the destination's
// current elements, preserving untouched elements and sibling defaults.
func Test_Env_AbsentSlicePreservesDefaults(t *testing.T) {
	type Gateway struct {
		Endpoint string `yaml:"endpoint"`
		Timeout  string `yaml:"timeout"`
	}
	type Config struct {
		Gateways []Gateway `yaml:"gateways"`
	}

	cfg := Config{Gateways: []Gateway{
		{Endpoint: "first", Timeout: "1s"},
		{Endpoint: "second", Timeout: "2s"},
	}}
	require.NoError(t, xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_1_ENDPOINT": "from-env"}),
	))
	require.Equal(t, []Gateway{
		{Endpoint: "first", Timeout: "1s"},
		{Endpoint: "from-env", Timeout: "2s"},
	}, cfg.Gateways)
}

// A present wrapper does not hide the destination slice whose defaults must
// seed an absent sequence before one element is overridden.
func Test_Env_OptionalSlicePreservesDefaults(t *testing.T) {
	type Gateway struct {
		Endpoint string `yaml:"endpoint"`
		Timeout  string `yaml:"timeout"`
	}
	type Config struct {
		Gateways xcfg.Optional[[]Gateway] `yaml:"gateways"`
	}

	cfg := Config{Gateways: xcfg.NewOptional([]Gateway{
		{Endpoint: "first", Timeout: "1s"},
		{Endpoint: "second", Timeout: "2s"},
	})}
	require.NoError(t, xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_1_ENDPOINT": "from-env"}),
	))
	require.Equal(t, []Gateway{
		{Endpoint: "first", Timeout: "1s"},
		{Endpoint: "from-env", Timeout: "2s"},
	}, *cfg.Gateways.Unwrap())
}

// A required wrapper likewise preserves its existing sequence while the
// environment explicitly supplies the overridden child.
func Test_Env_RequiredSlicePreservesDefaults(t *testing.T) {
	type Gateway struct {
		Endpoint string `yaml:"endpoint"`
		Timeout  string `yaml:"timeout"`
	}
	type Config struct {
		Gateways xcfg.Required[[]Gateway] `yaml:"gateways"`
	}

	cfg := Config{Gateways: xcfg.NewRequired([]Gateway{
		{Endpoint: "first", Timeout: "1s"},
		{Endpoint: "second", Timeout: "2s"},
	})}
	require.NoError(t, xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_1_ENDPOINT": "from-env"}),
	))
	require.Equal(t, []Gateway{
		{Endpoint: "first", Timeout: "1s"},
		{Endpoint: "from-env", Timeout: "2s"},
	}, cfg.Gateways.Unwrap())
}

// A value wrapper around a struct retains sibling defaults when an override
// materialises the otherwise absent mapping.
func Test_Env_RequiredStructPreservesDefaults(t *testing.T) {
	type Server struct {
		Endpoint string `yaml:"endpoint"`
		Timeout  string `yaml:"timeout"`
	}
	type Config struct {
		Server xcfg.Required[Server] `yaml:"server"`
	}

	cfg := Config{Server: xcfg.NewRequired(Server{Endpoint: "old", Timeout: "5s"})}
	require.NoError(t, xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_ENDPOINT": "from-env"}),
	))
	require.Equal(t, Server{Endpoint: "from-env", Timeout: "5s"}, cfg.Server.Unwrap())
}

// Materialising sequence defaults must not turn an unset required sibling
// into an explicitly supplied zero value.
func Test_Env_SequenceDefaultsKeepRequiredUnset(t *testing.T) {
	type Gateway struct {
		Endpoint   string             `yaml:"endpoint"`
		InstanceID xcfg.Required[int] `yaml:"instance_id"`
	}
	type Config struct {
		Gateways []Gateway `yaml:"gateways"`
	}

	cfg := Config{Gateways: []Gateway{{Endpoint: "from-default"}}}
	err := xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_0_ENDPOINT": "from-env"}),
	)
	require.ErrorContains(t, err, "value must be set explicitly")
}

// An exact environment value may satisfy an unset required element that was
// present only in a destination-defaulted sequence.
func Test_Env_SequenceDefaultRequiredElementOverridden(t *testing.T) {
	type Config struct {
		Values []xcfg.Required[int] `yaml:"values"`
	}

	cfg := Config{Values: make([]xcfg.Required[int], 1)}
	require.NoError(t, xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_VALUES_0": "3"}),
	))
	require.Equal(t, 3, cfg.Values[0].Unwrap())
}

// Explicit-presence state is checked through every wrapper layer when
// sequence defaults are materialised.
func Test_Env_SequenceDefaultsKeepNestedRequiredUnset(t *testing.T) {
	type Gateway struct {
		Endpoint   string                            `yaml:"endpoint"`
		InstanceID xcfg.Optional[xcfg.Required[int]] `yaml:"instance_id"`
	}
	type Config struct {
		Gateways []Gateway `yaml:"gateways"`
	}

	cfg := Config{Gateways: []Gateway{{
		Endpoint:   "from-default",
		InstanceID: xcfg.NewOptional(xcfg.Required[int]{}),
	}}}
	err := xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_GATEWAYS_0_ENDPOINT": "from-env"}),
	)
	require.ErrorContains(t, err, "unset required")
}

// Supplying one descendant of an unset required struct does not mark its own
// untouched required descendants as explicitly supplied.
func Test_Env_SequenceDefaultsKeepRequiredDescendantUnset(t *testing.T) {
	type Inner struct {
		Name string             `yaml:"name"`
		ID   xcfg.Required[int] `yaml:"id"`
	}
	type Config struct {
		Values []xcfg.Optional[xcfg.Required[Inner]] `yaml:"values"`
	}

	cfg := Config{Values: []xcfg.Optional[xcfg.Required[Inner]]{
		xcfg.NewOptional(xcfg.Required[Inner]{}),
	}}
	err := xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_VALUES_0_NAME": "from-env"}),
	)
	require.Error(t, err)
}

// An already present optional struct is preserved from its current value
// rather than recreated from only the overridden child.
func Test_Env_OptionalStructPreservesDefaults(t *testing.T) {
	type Server struct {
		Endpoint string `yaml:"endpoint"`
		Timeout  string `yaml:"timeout"`
	}
	type Config struct {
		Server xcfg.Optional[Server] `yaml:"server"`
	}

	cfg := Config{Server: xcfg.NewOptional(Server{Endpoint: "old", Timeout: "5s"})}
	require.NoError(t, xcfg.Decode(
		[]byte("{}\n"),
		&cfg,
		envOverrides(t, map[string]string{"YANET_SERVER_ENDPOINT": "from-env"}),
	))
	require.Equal(t, Server{Endpoint: "from-env", Timeout: "5s"}, *cfg.Server.Unwrap())
}

// An empty prefix names a sequence element without a leading underscore too.
func Test_Env_EmptyPrefixSequenceElement(t *testing.T) {
	type Gateway struct {
		Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	}
	type Config struct {
		Gateways []Gateway `yaml:"gateways"`
	}

	t.Setenv("GATEWAYS_0_ENDPOINT", "from-env")

	var cfg Config
	require.NoError(t, xcfg.Decode(
		[]byte("gateways:\n  - endpoint: from-file\n"),
		&cfg,
		xcfg.WithEnvPrefix(""),
	))
	require.Equal(t, "from-env", cfg.Gateways[0].Endpoint.Unwrap())
}
