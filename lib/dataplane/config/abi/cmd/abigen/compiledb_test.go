package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseCompileDBPrefersPICEntryOnDuplicatePath pins the -fPIC tie-break.
func TestParseCompileDBPrefersPICEntryOnDuplicatePath(t *testing.T) {
	tests := []struct {
		name    string
		entries []compileEntry
	}{
		{
			name: "PIC listed first",
			entries: []compileEntry{
				{Directory: "/build", File: "a.c", Command: "cc -fPIC -c a.c"},
				{Directory: "/build", File: "a.c", Command: "cc -c a.c"},
			},
		},
		{
			name: "PIC listed last",
			entries: []compileEntry{
				{Directory: "/build", File: "a.c", Command: "cc -c a.c"},
				{Directory: "/build", File: "a.c", Command: "cc -fPIC -c a.c"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.entries)
			require.NoError(t, err)
			db, err := parseCompileDB(data)
			require.NoError(t, err)
			entry, err := db.Lookup(filepath.Join("/build", "a.c"))
			require.NoError(t, err)
			require.True(t, isPositionIndependent(entry.Command), "parseCompileDB kept the non-PIC entry: %q", entry.Command)
		})
	}
}
