package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestShellSplit pins quoting and escaping in the compile-command splitter.
func TestShellSplit(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "simple flags",
			in:   "cc -Ilib -DFOO -c a.c -o a.o",
			want: []string{"cc", "-Ilib", "-DFOO", "-c", "a.c", "-o", "a.o"},
		},
		{
			name: "single quote preserves embedded double quotes",
			in:   `cc '-DABI_VERSION="24.1"' -c a.c`,
			want: []string{"cc", `-DABI_VERSION="24.1"`, "-c", "a.c"},
		},
		{
			name: "double quote with escaped quote",
			in:   `cc -DX="a\"b" -c a.c`,
			want: []string{"cc", `-DX=a"b`, "-c", "a.c"},
		},
		{
			name: "bare backslash escape",
			in:   `cc -DX=a\ b -c a.c`,
			want: []string{"cc", "-DX=a b", "-c", "a.c"},
		},
		{
			name: "collapses repeated whitespace",
			in:   "cc   -c  a.c",
			want: []string{"cc", "-c", "a.c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := shellSplit(tt.in)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestShellSplitUnterminatedQuotes pins the error for an unterminated quote.
func TestShellSplitUnterminatedQuotes(t *testing.T) {
	_, err := shellSplit(`cc -DX="unterminated`)
	require.Error(t, err, "unterminated double quote")
	_, err = shellSplit(`cc -DX='unterminated`)
	require.Error(t, err, "unterminated single quote")
}
