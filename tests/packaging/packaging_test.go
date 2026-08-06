// Package packaging_test guards against a shipped default config naming a
// /etc/yanet2 path that the debian packaging never installs there.
//
// Such a path decodes fine and looks correct in review, but points at a file
// that will not exist on a fresh install, so the gap only surfaces at
// runtime. The check walks each shipped YAML's parsed node tree rather than
// its raw text, because a shipped file may legitimately mention an
// uninstalled /etc/yanet2 path inside a commented-out example.
package packaging_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// etcYanet2Prefix is the path prefix a shipped config value must carry to be
// checked against the debian install manifests.
const etcYanet2Prefix = "/etc/yanet2/"

// shippedConfigPaths lists every default config YAML installed under
// /etc/yanet2, relative to the repository root.
//
// Some are shipped by the root Makefile's install target, others by an
// operator's meson install_data.
var shippedConfigPaths = []string{
	"controlplane/etc/yanet/controlplane-default.yaml",
	"dataplane.yaml",
	"operators/bird-adapter/etc/yanet/bird-adapter-default.yaml",
	"operators/pipeline/etc/yanet/yanet-pipeline-operator-default.yaml",
	"operators/route/etc/yanet/yanet-route-operator-default.yaml",
	"operators/decap/etc/yanet/yanet-decap-operator-default.yaml",
	"operators/decap/etc/yanet/decap.d/default.yaml",
	"operators/forward/etc/yanet/yanet-forward-operator-default.yaml",
	"operators/forward/etc/yanet/forward.d/vlan-phy-default.yaml",
	"operators/forward/etc/yanet/forward.d/phy-vlan-default.yaml",
}

// collectEtcYanet2Values walks node's mapping and sequence structure and
// returns every scalar value carrying etcYanet2Prefix.
//
// It never inspects comments: yaml.v3 attaches comment text to the node
// tree but not as a scalar value, so a path mentioned only in a
// commented-out example is invisible here.
func collectEtcYanet2Values(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.AliasNode {
		node = node.Alias
		if node == nil {
			return nil
		}
	}

	var found []string
	switch node.Kind {
	case yaml.ScalarNode:
		if strings.HasPrefix(node.Value, etcYanet2Prefix) {
			found = append(found, node.Value)
		}
	case yaml.MappingNode, yaml.SequenceNode, yaml.DocumentNode:
		for _, child := range node.Content {
			found = append(found, collectEtcYanet2Values(child)...)
		}
	}
	return found
}

// loadInstalledPatterns returns the source-path patterns listed across every
// debian/*.install manifest, one per line, matchable with filepath.Match.
//
// Each line's first whitespace-separated field is the source path; a
// debhelper .install line may carry a second field naming the destination
// directory, which this ignores.
func loadInstalledPatterns(t *testing.T) []string {
	t.Helper()

	matches, err := filepath.Glob("../../debian/*.install")
	require.NoError(t, err)
	require.NotEmpty(t, matches, "no debian/*.install manifests found")

	var patterns []string
	for _, path := range matches {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			patterns = append(patterns, strings.Fields(line)[0])
		}
	}
	return patterns
}

// isInstalled reports whether path matches one of patterns, glob or literal.
func isInstalled(patterns []string, path string) bool {
	for _, pattern := range patterns {
		if ok, err := filepath.Match(pattern, path); err == nil && ok {
			return true
		}
	}
	return false
}

// Test_ShippedEtcYanet2PathsAreInstalled verifies that every /etc/yanet2
// path named in a shipped default config is listed in some debian/*.install
// manifest.
func Test_ShippedEtcYanet2PathsAreInstalled(t *testing.T) {
	installed := loadInstalledPatterns(t)

	for _, configPath := range shippedConfigPaths {
		t.Run(configPath, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../..", configPath))
			require.NoError(t, err)

			var doc yaml.Node
			require.NoError(t, yaml.Unmarshal(data, &doc))
			if len(doc.Content) == 0 {
				return
			}

			for _, value := range collectEtcYanet2Values(doc.Content[0]) {
				installPath := strings.TrimPrefix(value, "/")
				require.True(t, isInstalled(installed, installPath),
					"%s: %q is not listed in any debian/*.install manifest", configPath, value)
			}
		})
	}
}

// Test_CommentedOutPathsAreNotFlagged pins the distinction between walking
// parsed YAML values and scanning raw text.
//
// controlplane-default.yaml carries commented-out TLS and auth examples
// naming /etc/yanet2 paths, none of which are installed. A raw-text scan
// would flag them. The node walk must not, since a commented-out path was
// never parsed as a value.
func Test_CommentedOutPathsAreNotFlagged(t *testing.T) {
	data, err := os.ReadFile("../../controlplane/etc/yanet/controlplane-default.yaml")
	require.NoError(t, err)

	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.NotEmpty(t, doc.Content)

	parsedValues := collectEtcYanet2Values(doc.Content[0])

	commentedOutPaths := []string{
		"/etc/yanet2/tls/server.pem",
		"/etc/yanet2/tls/server.key",
		"/etc/yanet2/auth/identities.yaml",
		"/etc/yanet2/auth/basic_auth.yaml",
		"/etc/yanet2/auth/permissions.yaml",
	}
	for _, path := range commentedOutPaths {
		require.NotContains(t, parsedValues, path,
			"a commented-out example must never surface as a parsed value")
	}

	rawMatches := regexp.MustCompile(regexp.QuoteMeta(etcYanet2Prefix)).FindAllString(string(data), -1)
	require.GreaterOrEqual(t, len(rawMatches), len(commentedOutPaths),
		"expected the commented-out auth/tls examples to still mention /etc/yanet2")
}
