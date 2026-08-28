package xproto_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xproto"
)

// verifies that a declared value renders as its name and an undeclared one
// as its number, so both survive a round trip through a peer.
func Test_MarshalEnumJSON_NameOrNumber(t *testing.T) {
	declared, err := xproto.MarshalEnumJSON(filterpb.FragmentKind_Frag)
	require.NoError(t, err)
	require.JSONEq(t, `"Frag"`, string(declared))

	undeclared, err := xproto.MarshalEnumJSON(filterpb.FragmentKind(7))
	require.NoError(t, err)
	require.JSONEq(t, `7`, string(undeclared))
}

// verifies that each accepted spelling lands on the intended value and each
// rejected one names the reason.
func Test_UnmarshalEnumJSON_Spellings(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    filterpb.FragmentKind
		wantErr string
	}{
		{name: "declared name", input: `"None"`, want: filterpb.FragmentKind_None},
		{name: "declared number", input: `1`, want: filterpb.FragmentKind_None},
		{name: "undeclared number is kept", input: `7`, want: filterpb.FragmentKind(7)},
		{name: "null leaves the seeded value", input: `null`, want: filterpb.FragmentKind_Frag},
		{name: "wrong letter case", input: `"none"`, wantErr: "want one of Any, None, Frag"},
		{name: "fraction", input: `2.5`, wantErr: "not a valid number"},
		{name: "out of int32 range", input: `4294967296`, wantErr: "not a valid number"},
		{name: "boolean", input: `true`, wantErr: "expected a name or a number"},
		{name: "object", input: `{"kind": 2}`, wantErr: "expected a name or a number"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Seeded with a nonzero value that no case decodes to, so a decode
			// that resets or ignores the target is caught either way.
			kind := filterpb.FragmentKind_Frag
			err := xproto.UnmarshalEnumJSON([]byte(tc.input), &kind)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, kind)
		})
	}
}
