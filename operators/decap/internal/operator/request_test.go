package operator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/decap/internal/operator"
)

// writeModuleConfig stores data as a module config file in a fresh
// directory and returns its path.
func writeModuleConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "decap.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
	return path
}

// Test_LoadModuleConfig_FamilyTypedLists verifies that the lists load in
// document order and a file without a name takes the function's module.
func Test_LoadModuleConfig_FamilyTypedLists(t *testing.T) {
	path := writeModuleConfig(t, `
prefixes4:
  - 10.0.0.0/8
  - 192.0.2.0/24
prefixes6:
  - 2001:db8::/32
`)

	request, err := operator.LoadModuleConfig(path, "decap0")
	require.NoError(t, err)

	require.Equal(t, "decap0", request.GetName())
	require.Len(t, request.GetPrefixes4(), 2)
	require.Equal(t, "10.0.0.0/8", request.GetPrefixes4()[0].AsLogValue())
	require.Equal(t, "192.0.2.0/24", request.GetPrefixes4()[1].AsLogValue())
	require.Len(t, request.GetPrefixes6(), 1)
	require.Equal(t, "2001:db8::/32", request.GetPrefixes6()[0].AsLogValue())
}

// Test_LoadModuleConfig_MatchingNameInFile verifies that a file naming the
// function's own config loads as is.
func Test_LoadModuleConfig_MatchingNameInFile(t *testing.T) {
	path := writeModuleConfig(t, "name: decap0\nprefixes4: [10.0.0.0/8]\n")

	request, err := operator.LoadModuleConfig(path, "decap0")
	require.NoError(t, err)

	require.Equal(t, "decap0", request.GetName())
}

// Test_LoadModuleConfig_RejectsNameMismatch verifies that a file naming
// another config is rejected instead of being rebound to the function.
func Test_LoadModuleConfig_RejectsNameMismatch(t *testing.T) {
	path := writeModuleConfig(t, "name: other\nprefixes4: [10.0.0.0/8]\n")

	_, err := operator.LoadModuleConfig(path, "decap0")

	require.ErrorContains(t, err, `names config "other"`)
	require.ErrorContains(t, err, `targets "decap0"`)
}

// Test_LoadModuleConfig_RejectsWrongFamilyEntry verifies that an entry of
// the wrong family fails the whole load, naming the entry.
func Test_LoadModuleConfig_RejectsWrongFamilyEntry(t *testing.T) {
	path := writeModuleConfig(t, "prefixes4:\n  - 2001:db8::/32\n")

	_, err := operator.LoadModuleConfig(path, "decap0")

	require.ErrorContains(t, err, "not a valid IPv4 prefix: 2001:db8::/32")
}

// Test_LoadModuleConfig_RejectsUnknownKey verifies that a key outside the
// request, such as the web export's config key, is rejected, not ignored.
func Test_LoadModuleConfig_RejectsUnknownKey(t *testing.T) {
	path := writeModuleConfig(t, "config: decap0\nprefixes4: []\n")

	_, err := operator.LoadModuleConfig(path, "decap0")

	require.ErrorContains(t, err, `unknown field "config"`)
}

// Test_LoadModuleConfig_ReportsMissingFile verifies that an unreadable
// file fails with its path in the error.
func Test_LoadModuleConfig_ReportsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")

	_, err := operator.LoadModuleConfig(path, "decap0")

	require.ErrorContains(t, err, path)
}

// Test_LoadModuleConfig_RejectsEmptyPrefixEntry verifies that an empty
// list entry fails the load instead of reaching the gateway as no prefix.
func Test_LoadModuleConfig_RejectsEmptyPrefixEntry(t *testing.T) {
	path := writeModuleConfig(t, "prefixes4: [10.0.0.0/8, null]\n")

	_, err := operator.LoadModuleConfig(path, "decap0")

	require.ErrorContains(t, err, "prefixes4[1] is null")
}
