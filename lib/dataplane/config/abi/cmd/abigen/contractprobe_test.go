package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSelectContractTemplateRejectsMacroAbsentFromOtherUnit pins the subset check.
func TestSelectContractTemplateRejectsMacroAbsentFromOtherUnit(t *testing.T) {
	entries := []compileEntry{
		{Directory: "/repo", Command: "cc -DFOO=1 -c a.c", File: "a.c"},
		{Directory: "/repo", Command: "cc -c b.c", File: "b.c"},
	}

	_, err := selectContractTemplate(entries)
	require.Error(t, err)
	require.ErrorContains(t, err, "-DFOO")
	require.ErrorContains(t, err, "b.c")
}

// TestSelectContractTemplateAcceptsSharedMacros pins the pass case.
func TestSelectContractTemplateAcceptsSharedMacros(t *testing.T) {
	entries := []compileEntry{
		{Directory: "/repo", Command: "cc -DFOO=1 -DBAR -c a.c", File: "a.c"},
		{Directory: "/repo", Command: "cc -DFOO=1 -DBAR -DBAZ=2 -c b.c", File: "b.c"},
	}

	entry, err := selectContractTemplate(entries)
	require.NoError(t, err)
	require.Equal(t, "a.c", entry.File)
}
