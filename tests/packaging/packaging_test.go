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
	"os/exec"
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
// Each is installed by a meson install_data rule, not copied by hand in
// the root Makefile.
var shippedConfigPaths = []string{
	"controlplane/etc/yanet/controlplane.d/default.yaml",
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

// Test_ControlplaneTemplateConfigIsInstalled verifies that the config the
// controlplane template unit executes is installed for the default instance.
//
// The template resolves its config path from the instance name, so renaming
// the shipped file or changing its install_dir breaks the packaged service
// while every check that walks config contents stays green.
func Test_ControlplaneTemplateConfigIsInstalled(t *testing.T) {
	data, err := os.ReadFile("../../debian/yanet2-controlplane@.service")
	require.NoError(t, err)

	match := regexp.MustCompile(`(?m)^ExecStart=.* -c (\S+)$`).FindStringSubmatch(string(data))
	require.Len(t, match, 2, "no ExecStart config path in the controlplane template unit")

	configPath := strings.ReplaceAll(match[1], "%i", "default")
	require.True(t, isInstalled(loadInstalledPatterns(t), strings.TrimPrefix(configPath, "/")),
		"%q is not listed in any debian/*.install manifest", configPath)
}

// Test_CommentedOutPathsAreNotFlagged pins the distinction between walking
// parsed YAML values and scanning raw text.
//
// controlplane.d/default.yaml carries commented-out TLS and auth examples
// naming /etc/yanet2 paths, none of which are installed. A raw-text scan
// would flag them. The node walk must not, since a commented-out path was
// never parsed as a value.
func Test_CommentedOutPathsAreNotFlagged(t *testing.T) {
	data, err := os.ReadFile("../../controlplane/etc/yanet/controlplane.d/default.yaml")
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

func Test_SetupExampleUsesSourceTreeForwardFlow(t *testing.T) {
	temporaryRoot := filepath.Join(t.TempDir(), "source tree with spaces")
	scriptDirectory := filepath.Join(temporaryRoot, "scripts")
	stubDirectory := filepath.Join(t.TempDir(), "bin")
	logPath := filepath.Join(t.TempDir(), "calls.log")
	require.NoError(t, os.MkdirAll(scriptDirectory, 0o755))
	require.NoError(t, os.MkdirAll(stubDirectory, 0o755))

	script, err := os.ReadFile("../../scripts/setup-example.sh")
	require.NoError(t, err)
	scriptPath := filepath.Join(scriptDirectory, "setup-example.sh")
	require.NoError(t, os.WriteFile(scriptPath, script, 0o755))
	forwardPath := filepath.Join(temporaryRoot, "forward.yaml")
	require.NoError(t, os.WriteFile(forwardPath, nil, 0o644))
	resolvedRoot, err := filepath.EvalSymlinks(temporaryRoot)
	require.NoError(t, err)
	resolvedForwardPath := filepath.Join(resolvedRoot, "forward.yaml")

	stub := []byte("#!/bin/sh\nprintf '%s' \"${0##*/}\" >> \"$YANET_TEST_LOG\"\nfor argument do\n    printf '\\t%s' \"$argument\" >> \"$YANET_TEST_LOG\"\ndone\nprintf '\\n' >> \"$YANET_TEST_LOG\"\n")
	for _, name := range []string{
		"yanet-cli-forward",
		"yanet-cli-pipeline",
		"yanet-cli-function",
		"yanet-cli-device-plain",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(stubDirectory, name), stub, 0o755))
	}

	environment := make([]string, 0, len(os.Environ())+2)
	for _, variable := range os.Environ() {
		if strings.HasPrefix(variable, "PATH=") {
			continue
		}
		environment = append(environment, variable)
	}
	environment = append(environment,
		"PATH="+stubDirectory+string(os.PathListSeparator)+os.Getenv("PATH"),
		"YANET_TEST_LOG="+logPath,
	)
	command := exec.Command(scriptPath)
	command.Env = environment
	require.NoError(t, command.Run())

	recorded, err := os.ReadFile(logPath)
	require.NoError(t, err)
	expected := strings.Join([]string{
		"yanet-cli-forward\tupdate\t--name=forward0\t--rules\t" + resolvedForwardPath,
		"yanet-cli-pipeline\tupdate\t--name=dummy",
		"yanet-cli-function\tupdate\t--name=virt\t--chains\tchain0:10=forward:forward0",
		"yanet-cli-pipeline\tupdate\t--name=virt\t--functions\tvirt",
		"yanet-cli-device-plain\tupdate\t--name=virtio_user_kni0\t--input\tvirt:1\t--output\tdummy:1",
		"yanet-cli-function\tupdate\t--name=phy\t--chains\tchain0:10=forward:forward0",
		"yanet-cli-pipeline\tupdate\t--name=phy\t--functions\tphy",
		"yanet-cli-device-plain\tupdate\t--name=01:00.0\t--input\tphy:1\t--output\tdummy:1",
	}, "\n") + "\n"
	require.Equal(t, expected, string(recorded))
}
