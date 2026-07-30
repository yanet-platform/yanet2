package ledger_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/lint/internal/ledger"
)

// TestLoadMissingFileIsEmpty verifies that loading a nonexistent allowlist
// file yields an empty, clean ledger rather than an error.
func TestLoadMissingFileIsEmpty(t *testing.T) {
	allowlist, err := ledger.Load(filepath.Join(t.TempDir(), "missing.txt"))
	require.NoError(t, err)

	var buf bytes.Buffer
	require.False(t, allowlist.Report(&buf))
	require.Empty(t, buf.String())
}

// TestSuppressesMatchesEntry verifies that a well-formed entry with a
// reason suppresses its matching key and leaves the ledger clean.
func TestSuppressesMatchesEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.txt")
	require.NoError(t, os.WriteFile(path, []byte(
		"foo.go:NewFoo  # legacy: migrate to the options pattern\n",
	), 0o644))

	allowlist, err := ledger.Load(path)
	require.NoError(t, err)
	require.True(t, allowlist.Suppresses("foo.go:NewFoo"))

	var buf bytes.Buffer
	require.False(t, allowlist.Report(&buf))
	require.Empty(t, buf.String())
}

// TestReportStaleEntryFails verifies that an entry with no matching
// Suppresses call is reported as a stale-entry failure.
func TestReportStaleEntryFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.txt")
	require.NoError(t, os.WriteFile(path, []byte(
		"foo.go:NewFoo  # legacy: migrate to the options pattern\n",
	), 0o644))

	allowlist, err := ledger.Load(path)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.True(t, allowlist.Report(&buf))
	require.Contains(t, buf.String(), "stale entry foo.go:NewFoo")
}

// TestLoadMissingReasonIsAnIssue verifies that an entry without a "#
// reason" suffix does not suppress its matching key and is itself reported
// as an issue, since a reason is mandatory.
func TestLoadMissingReasonIsAnIssue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.txt")
	require.NoError(t, os.WriteFile(path, []byte(
		"foo.go:NewFoo\n",
	), 0o644))

	allowlist, err := ledger.Load(path)
	require.NoError(t, err)
	require.False(t, allowlist.Suppresses("foo.go:NewFoo"))

	var buf bytes.Buffer
	require.True(t, allowlist.Report(&buf))
	require.Contains(t, buf.String(), "is missing a mandatory reason")
}

// TestSuppressesMatchesKeyContainingHash verifies that a key embedding a
// bare "#" — as a zapmsg key does, since it carries the raw log message —
// is not truncated at that "#" as long as the row's reason is introduced by
// the canonical two-space separator.
func TestSuppressesMatchesKeyContainingHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.txt")
	require.NoError(t, os.WriteFile(path, []byte(
		"zapmsg:foo.go:Run:Info:Worker #3 failed:1  # legacy: lowercase the message\n",
	), 0o644))

	allowlist, err := ledger.Load(path)
	require.NoError(t, err)
	require.True(t, allowlist.Suppresses("zapmsg:foo.go:Run:Info:Worker #3 failed:1"))

	var buf bytes.Buffer
	require.False(t, allowlist.Report(&buf))
	require.Empty(t, buf.String())
}

// TestLoadDuplicateEntryIsAnIssue verifies that a repeated allowlist key is
// reported as an issue instead of silently overwriting the earlier row, so
// a duplicated ledger entry cannot hide from review.
func TestLoadDuplicateEntryIsAnIssue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.txt")
	require.NoError(t, os.WriteFile(path, []byte(
		"foo.go:NewFoo  # legacy: migrate to the options pattern\n"+
			"foo.go:NewFoo  # duplicate row\n",
	), 0o644))

	allowlist, err := ledger.Load(path)
	require.NoError(t, err)
	require.True(t, allowlist.Suppresses("foo.go:NewFoo"))

	var buf bytes.Buffer
	require.True(t, allowlist.Report(&buf))
	require.Contains(t, buf.String(), "duplicate entry foo.go:NewFoo, already defined on line 1")
}
