package xcfg_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// writeConfig creates an isolated configuration source at a relative path.
func writeConfig(t *testing.T, directory, name, content string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// Test_ReadFile_Merge verifies that composition preserves scalar types,
// replaces lists and retains sibling keys through nested mappings.
func Test_ReadFile_Merge(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, directory, "nested/first.yaml", `
include: [../base.yaml]
server: {port: 8443, enabled: false}
items: [removed]
`)
	writeConfig(t, directory, "base.yaml", `
server: {name: original}
items: [replacement]
`)
	path := writeConfig(t, directory, "main.yaml", `
include: [nested/first.yaml]
server: {name: local, port: 80, enabled: true}
items: [main]
`)
	data, err := xcfg.ReadFile(path)
	require.NoError(t, err)
	var config struct {
		Server struct {
			Name    string `yaml:"name"`
			Port    int    `yaml:"port"`
			Enabled bool   `yaml:"enabled"`
		} `yaml:"server"`
		Items []string `yaml:"items"`
	}
	require.NoError(t, yaml.Unmarshal(data, &config))
	require.Equal(t, "original", config.Server.Name)
	require.Equal(t, 8443, config.Server.Port)
	require.False(t, config.Server.Enabled)
	require.Equal(t, []string{"replacement"}, config.Items)
}

// Test_ReadFile_ScalarSpelling verifies that include composition preserves
// the string representation accepted by the typed destination.
func Test_ReadFile_ScalarSpelling(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scalar string
		value  string
	}{
		{name: "leading zero integer", scalar: "00123", value: "00123"},
		{name: "hexadecimal integer", scalar: "0xFF", value: "0xFF"},
		{name: "scientific notation", scalar: "1e+03", value: "1e+03"},
		{name: "fraction precision", scalar: "1.2300", value: "1.2300"},
		{name: "uppercase boolean", scalar: "TRUE", value: "TRUE"},
		{name: "explicit binary", scalar: "!!binary aGVsbG8=", value: "hello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type config struct {
				Main     string            `yaml:"main"`
				Included string            `yaml:"included"`
				Items    []string          `yaml:"items"`
				Labels   map[string]string `yaml:"labels"`
			}
			directory := t.TempDir()
			writeConfig(t, directory, "fragment.yaml", "included: "+tc.scalar+"\n")
			path := writeConfig(t, directory, "main.yaml",
				"include: [fragment.yaml]\nmain: "+tc.scalar+
					"\nitems: ["+tc.scalar+"]\nlabels: {value: "+tc.scalar+"}\n",
			)
			loaded, err := xcfg.LoadConfig[config](path)
			require.NoError(t, err)
			require.Equal(t, &config{
				Main: tc.value, Included: tc.value,
				Items: []string{tc.value}, Labels: map[string]string{"value": tc.value},
			}, loaded)
		})
	}
}

// Test_ReadFile_OptionalIncludesKeepStringKeys verifies that optional sources
// retain names used for exact account and permission lookups.
func Test_ReadFile_OptionalIncludesKeepStringKeys(t *testing.T) {
	for _, include := range []string{"[]", "[missing.yaml]", "[empty.yaml]", "[fragment.yaml]"} {
		t.Run(include, func(t *testing.T) {
			directory := t.TempDir()
			writeConfig(t, directory, "empty.yaml", "")
			writeConfig(t, directory, "fragment.yaml", "retained: true\n")
			path := writeConfig(t, directory, "main.yaml",
				"include: "+include+"\nidentities: [{username: 00123}]\npermissions: {00123: read}\n",
			)
			var config struct {
				Identities []struct {
					Username string `yaml:"username"`
				} `yaml:"identities"`
				Permissions map[string]string `yaml:"permissions"`
			}
			data, err := xcfg.ReadFile(path)
			require.NoError(t, err)
			require.NoError(t, yaml.Unmarshal(data, &config))
			require.Len(t, config.Identities, 1)
			require.Equal(t, "00123", config.Identities[0].Username)
			require.Equal(t, map[string]string{"00123": "read"}, config.Permissions)
		})
	}
}

// Test_ReadFile_AliasesMergeKeysAndNulls verifies that composition keeps YAML
// merge precedence and explicit nulls while resolving file-local aliases.
func Test_ReadFile_AliasesMergeKeysAndNulls(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, directory, "fragment.yaml", `
defaults: &defaults {name: 00123, enabled: true}
settings: {<<: *defaults, enabled: false}
items: [null, 00123, null]
reset: null
`)
	path := writeConfig(t, directory, "main.yaml", "include: [fragment.yaml]\nreset: previous\n")
	data, err := xcfg.ReadFile(path)
	require.NoError(t, err)
	require.YAMLEq(t, `
defaults: {name: 00123, enabled: true}
settings: {name: 00123, enabled: false}
items: [null, 00123, null]
reset: null
`, string(data))
}

// Test_ReadFile_IncludeOrder verifies that optional overlays replace main-file
// defaults in list order regardless of the directive's position.
func Test_ReadFile_IncludeOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		main string
	}{
		{"directive first", "include: [first.yaml, missing.yaml, empty, second.yaml]\nserver: {endpoint: generated, enabled: true, port: 80}\nitems: [generated]\n"},
		{"directive last", "server: {endpoint: generated, enabled: true, port: 80}\nitems: [generated]\ninclude: [first.yaml, missing.yaml, empty, second.yaml]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(directory, "empty"), 0o700))
			writeConfig(t, directory, "first.yaml", "server: {endpoint: first, enabled: false, port: 0}\nitems: [first]\n")
			writeConfig(t, directory, "second.yaml", "server: {endpoint: last}\nitems: []\n")
			path := writeConfig(t, directory, "main.yaml", tc.main)
			data, err := xcfg.ReadFile(path)
			require.NoError(t, err)
			require.YAMLEq(t, "server: {endpoint: last, enabled: false, port: 0}\nitems: []\n", string(data))
		})
	}
}

// Test_ReadFile_OptionalIncludes verifies that absent and empty overrides
// preserve the main configuration.
func Test_ReadFile_OptionalIncludes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		include     string
		files       map[string]string
		directories []string
	}{
		{name: "empty list", include: "[]"},
		{name: "empty directive", include: ""},
		{name: "null directive", include: "null"},
		{name: "missing file", include: "[missing.yaml]"},
		{name: "missing parent directory", include: "[missing/override.yaml]"},
		{name: "missing directory", include: "[conf.d]"},
		{name: "empty file", include: "[override.yaml]", files: map[string]string{"override.yaml": ""}},
		{name: "comment-only file", include: "[override.yaml]", files: map[string]string{"override.yaml": "# optional overrides\n"}},
		{name: "empty document", include: "[override.yaml]", files: map[string]string{"override.yaml": "---\nnull\n---\n"}},
		{name: "empty mapping", include: "[override.yaml]", files: map[string]string{"override.yaml": "{}\n"}},
		{name: "empty directory", include: "[conf.d]", directories: []string{"conf.d"}},
		{name: "nested empty directories", include: "[conf.d]", directories: []string{"conf.d/nested"}},
		{name: "directory without YAML", include: "[conf.d]", files: map[string]string{"conf.d/readme.txt": "not: [YAML"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			for name, content := range tc.files {
				writeConfig(t, directory, name, content)
			}
			for _, name := range tc.directories {
				require.NoError(t, os.MkdirAll(filepath.Join(directory, name), 0o700))
			}
			path := writeConfig(t, directory, "main.yaml", "include: "+tc.include+"\nserver: {endpoint: generated}\n")
			data, err := xcfg.ReadFile(path)
			require.NoError(t, err)
			require.YAMLEq(t, "server: {endpoint: generated}\n", string(data))
		})
	}
}

// Test_ReadFile_MissingMain verifies that only include sources are optional.
func Test_ReadFile_MissingMain(t *testing.T) {
	_, err := xcfg.ReadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// Test_ReadFile_InvalidInclude verifies that malformed sources still fail
// before their values can be accepted.
func Test_ReadFile_InvalidInclude(t *testing.T) {
	cases := []struct{ name, main, fragment, message string }{
		{"requires list", "include: fragment.yaml\n", "{}", "list of paths"},
		{"requires string", "include: [1]\n", "{}", "non-empty strings"},
		{"rejects empty path", "include: ['']\n", "{}", "non-empty strings"},
		{"rejects non-directory parent", "include: [fragment.yaml/override.yaml]\n", "{}", "fragment.yaml/override.yaml"},
		{"rejects duplicate keys", "include: [fragment.yaml]\n", "key: 1\nkey: 2\n", "already defined"},
		{"rejects malformed YAML", "include: [fragment.yaml]\n", "key: [", "fragment.yaml"},
		{"rejects second document", "include: [fragment.yaml]\n", "key: 1\n---\nkey: 2\n", "single YAML document"},
		{"rejects non-mapping root", "include: [fragment.yaml]\n", "[one, two]\n", "must be a mapping"},
		{"detects file cycle", "include: [fragment.yaml]\n", "include: [main.yaml]\n", "include cycle"},
		{"rejects cyclic alias", "include: [fragment.yaml]\n", "key: &cycle [*cycle]\n", "anchor 'cycle' value contains itself"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			writeConfig(t, directory, "fragment.yaml", tc.fragment)
			path := writeConfig(t, directory, "main.yaml", tc.main)
			_, err := xcfg.ReadFile(path)
			require.ErrorContains(t, err, tc.message)
			require.ErrorContains(t, err, "main.yaml")
		})
	}
}

// Test_ReadFile_SymlinkCycle verifies that alternate names cannot hide a cycle.
func Test_ReadFile_SymlinkCycle(t *testing.T) {
	directory := t.TempDir()
	path := writeConfig(t, directory, "main.yaml", "include: [alias.yaml]\n")
	require.NoError(t, os.Symlink("main.yaml", filepath.Join(directory, "alias.yaml")))
	_, err := xcfg.ReadFile(path)
	require.ErrorContains(t, err, "include cycle")
}

// Test_ReadFile_Bounds verifies that acyclic composition cannot grow without
// bound through repeated sources or long chains.
func Test_ReadFile_Bounds(t *testing.T) {
	t.Run("file count", func(t *testing.T) {
		directory := t.TempDir()
		writeConfig(t, directory, "fragment.yaml", "{}\n")
		path := writeConfig(t, directory, "main.yaml", "include: ["+strings.Repeat("fragment.yaml,", 256)+"]\n")
		_, err := xcfg.ReadFile(path)
		require.ErrorContains(t, err, "256 files")
	})
	t.Run("depth", func(t *testing.T) {
		directory := t.TempDir()
		for idx := range 34 {
			writeConfig(t, directory, fmt.Sprintf("%d.yaml", idx), fmt.Sprintf("include: [%d.yaml]\n", idx+1))
		}
		_, err := xcfg.ReadFile(filepath.Join(directory, "0.yaml"))
		require.ErrorContains(t, err, "include depth")
	})
}

// Test_ReadFile_NoIncludesRetainsBytes verifies that ordinary files retain
// their comments, anchors and source line numbers.
func Test_ReadFile_NoIncludesRetainsBytes(t *testing.T) {
	content := "# ordinary config\nname: &value original\nother: *value\n"
	path := writeConfig(t, t.TempDir(), "main.yaml", content)
	data, err := xcfg.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, content, string(data))
}

// Test_ReadFile_UncomposedValues verifies that the file reader preserves
// ordinary sequence documents and a trailing empty document.
func Test_ReadFile_UncomposedValues(t *testing.T) {
	for _, content := range []string{"[one, two]\n", "name: ordinary\n---\n"} {
		path := writeConfig(t, t.TempDir(), "main.yaml", content)
		data, err := xcfg.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, content, string(data))
	}
}

// Test_ReadFile_MixedMappingKeys verifies that overlays retain both numeric
// and string-keyed siblings.
func Test_ReadFile_MixedMappingKeys(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, directory, "base.yaml", "labels: {retained: true, override: old}\n")
	path := writeConfig(t, directory, "main.yaml", "include: [base.yaml]\nlabels: {1: numeric, override: new}\n")
	data, err := xcfg.ReadFile(path)
	require.NoError(t, err)
	var config struct {
		Labels map[any]any `yaml:"labels"`
	}
	require.NoError(t, yaml.Unmarshal(data, &config))
	require.Equal(t, map[any]any{"retained": true, "override": "old", 1: "numeric"}, config.Labels)
}

// Test_ReadFile_ByteLimit verifies that cumulative input is bounded even when
// each individual source fits within the limit.
func Test_ReadFile_ByteLimit(t *testing.T) {
	directory := t.TempDir()
	writeConfig(t, directory, "large.yaml", "#"+strings.Repeat("x", 8<<20)+"\n")
	path := writeConfig(t, directory, "main.yaml", "include: [large.yaml, large.yaml]\n")
	_, err := xcfg.ReadFile(path)
	require.ErrorContains(t, err, "16777216 bytes")
}

// Fuzz_ReadFile_Composition verifies that arbitrary fragment values survive
// composition while the main document supplies an otherwise absent default.
func Fuzz_ReadFile_Composition(f *testing.F) {
	f.Add("settings: {enabled: false, port: 8443}\nitems: [0, null, yes]\n")
	f.Add("key: &value [one, two]\nother: *value\n")
	f.Add("local_override: fragment\n")
	f.Fuzz(func(t *testing.T, content string) {
		if len(content) > 16<<10 {
			t.Skip()
		}
		var fragment map[string]any
		if yaml.Unmarshal([]byte(content), &fragment) != nil || fragment == nil {
			return
		}
		// Generated inputs may not select filesystem paths outside the fixture.
		delete(fragment, "include")
		data, err := yaml.Marshal(fragment)
		require.NoError(t, err)
		directory := t.TempDir()
		writeConfig(t, directory, "fragment.yaml", string(data))
		path := writeConfig(t, directory, "main.yaml", "include: [fragment.yaml]\nlocal_override: main\n")
		composed, err := xcfg.ReadFile(path)
		require.NoError(t, err)
		if _, exists := fragment["local_override"]; !exists {
			fragment["local_override"] = "main"
		}
		expected, err := yaml.Marshal(fragment)
		require.NoError(t, err)
		require.YAMLEq(t, string(expected), string(composed))
	})
}
