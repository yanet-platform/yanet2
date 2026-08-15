package ffi_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// A NUL in a pattern is refused rather than truncated on the way into C.
func TestValidateQueryRejectsNUL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		query   []string
		wantErr bool
	}{
		{name: "nil", query: nil},
		{name: "empty", query: []string{}},
		{name: "plain name", query: []string{"acl_no_match"}},
		{name: "pattern", query: []string{"acl_.*", "rule [0-9]+"}},
		{
			name:    "truncates to a catch-all",
			query:   []string{".*\x00xyz"},
			wantErr: true,
		},
		{
			name:    "truncates to an exact name",
			query:   []string{"rx\x00.*"},
			wantErr: true,
		},
		{
			name:    "trailing NUL",
			query:   []string{"acl_.*", "rx\x00"},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ffi.ValidateQuery(tc.query)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			require.True(t, errors.Is(err, ffi.ErrInvalidQuery))
		})
	}
}
