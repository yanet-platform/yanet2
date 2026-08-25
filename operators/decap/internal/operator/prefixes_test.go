package operator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/decap/internal/operator"
)

// TestLoadDecapPrefixes_FamilyTypedLists verifies that the family-typed
// lists load into their matching message lists, order preserved.
func TestLoadDecapPrefixes_FamilyTypedLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefixes.yaml")
	data := "prefixes4:\n  - 10.0.0.0/8\n  - 192.0.2.0/24\nprefixes6:\n  - 2001:db8::/32\n"
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))

	v4, v6, err := operator.LoadDecapPrefixes(path)
	require.NoError(t, err)

	require.Len(t, v4, 2)
	require.Equal(t, "10.0.0.0/8", v4[0].AsLogValue())
	require.Equal(t, "192.0.2.0/24", v4[1].AsLogValue())
	require.Len(t, v6, 1)
	require.Equal(t, "2001:db8::/32", v6[0].AsLogValue())
}

// TestLoadDecapPrefixes_RejectsWrongFamilyEntry verifies that an entry in
// the wrong family's list fails the whole load instead of being dropped.
func TestLoadDecapPrefixes_RejectsWrongFamilyEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefixes.yaml")
	data := "prefixes4:\n  - 2001:db8::/32\n"
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))

	_, _, err := operator.LoadDecapPrefixes(path)
	require.ErrorContains(t, err, "prefixes4[0]")
}
