package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestClassifySubprogram covers every DWARF shape readSubprogramEntry handles.
func TestClassifySubprogram(t *testing.T) {
	tests := []struct {
		name string
		f    subprogramFacts
		want bool
	}{
		{
			name: "plain declaration is rejected",
			f: subprogramFacts{
				IsDeclaration: true, HasOwnName: true, OriginAvailable: true,
				OriginExternal: true, OriginName: "foo", DefinedInSymtab: true,
			},
			want: false,
		},
		{
			name: "unnamed concrete instance with no abstract origin is rejected",
			f:    subprogramFacts{HasOwnName: false, OriginAvailable: false},
			want: false,
		},
		{
			name: "inlined abstract instance resolved through abstract origin is accepted",
			f: subprogramFacts{
				HasOwnName: false, OriginAvailable: true,
				OriginExternal: true, OriginName: "inlined_fn", DefinedInSymtab: true,
			},
			want: true,
		},
		{
			name: "inlined abstract instance not confirmed by the symbol table is rejected",
			f: subprogramFacts{
				HasOwnName: false, OriginAvailable: true,
				OriginExternal: true, OriginName: "inlined_fn", DefinedInSymtab: false,
			},
			want: false,
		},
		{
			name: "gcc identical-code-folding DIE carrying its own name and symbol is accepted",
			f: subprogramFacts{
				HasOwnName: true, OriginAvailable: true,
				OriginExternal: true, OriginName: "icf_fn", DefinedInSymtab: true,
			},
			want: true,
		},
		{
			name: "declaration-only prototype with a name but no defining symbol is rejected",
			f: subprogramFacts{
				HasOwnName: true, OriginAvailable: true,
				OriginExternal: true, OriginName: "prototype_only", DefinedInSymtab: false,
			},
			want: false,
		},
		{
			name: "static function is rejected regardless of the symbol table",
			f: subprogramFacts{
				HasOwnName: true, OriginAvailable: true,
				OriginExternal: false, OriginName: "static_fn", DefinedInSymtab: true,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, classifySubprogram(tt.f))
		})
	}
}
