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
items: [replacement]
`)
	writeConfig(t, directory, "base.yaml", `
server: {name: original, port: 443, enabled: true}
items: [removed]
`)
	path := writeConfig(t, directory, "main.yaml", `
include: [nested/first.yaml]
server: {name: local}
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
	require.Equal(t, "local", config.Server.Name)
	require.Equal(t, 8443, config.Server.Port)
	require.False(t, config.Server.Enabled)
	require.Equal(t, []string{"replacement"}, config.Items)
}

// Test_ReadFile_InvalidInclude verifies that missing or malformed sources fail
// before an incomplete configuration can be accepted.
func Test_ReadFile_InvalidInclude(t *testing.T) {
	cases := []struct{ name, main, fragment, message string }{
		{"requires list", "include: fragment.yaml\n", "{}", "list of paths"},
		{"rejects null", "include:\n", "{}", "list of paths"},
		{"requires string", "include: [1]\n", "{}", "non-empty strings"},
		{"rejects empty path", "include: ['']\n", "{}", "non-empty strings"},
		{"rejects missing file", "include: [missing.yaml]\n", "{}", "missing.yaml"},
		{"rejects duplicate keys", "include: [fragment.yaml]\n", "key: 1\nkey: 2\n", "already defined"},
		{"rejects malformed YAML", "include: [fragment.yaml]\n", "key: [", "fragment.yaml"},
		{"rejects second document", "include: [fragment.yaml]\n", "key: 1\n---\nkey: 2\n", "single YAML document"},
		{"rejects non-mapping root", "include: [fragment.yaml]\n", "[one, two]\n", "must be a mapping"},
		{"detects file cycle", "include: [fragment.yaml]\n", "include: [main.yaml]\n", "include cycle"},
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
	t.Run("empty directory", func(t *testing.T) {
		directory := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(directory, "empty"), 0o700))
		path := writeConfig(t, directory, "main.yaml", "include: [empty]\n")
		_, err := xcfg.ReadFile(path)
		require.ErrorContains(t, err, "no YAML files")
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

// Test_ReadFile_MixedMappingKeys verifies that adding a numeric key does not
// discard string-keyed siblings in an earlier mapping.
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
	require.Equal(t, map[any]any{"retained": true, "override": "new", 1: "numeric"}, config.Labels)
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
// composition while the main document replaces one top-level value.
func Fuzz_ReadFile_Composition(f *testing.F) {
	f.Add("settings: {enabled: false, port: 8443}\nitems: [0, null, yes]\n")
	f.Add("key: &value [one, two]\nother: *value\n")
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
		fragment["local_override"] = "main"
		expected, err := yaml.Marshal(fragment)
		require.NoError(t, err)
		require.YAMLEq(t, string(expected), string(composed))
	})
}
