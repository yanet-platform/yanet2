package shm_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// Test_String_StopsAtTerminator verifies that a C character array reads up
// to its first NUL, or whole when it has none.
func Test_String_StopsAtTerminator(t *testing.T) {
	cases := []struct {
		name  string
		field []byte
		want  string
	}{
		{"terminated with bytes after the terminator", []byte("abc\x00def"), "abc"},
		{"filled without a terminator", []byte("abcdef"), "abcdef"},
		{"empty string", []byte("\x00abc"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, shm.String(tc.field))
		})
	}
}

// Test_CopyString_TruncatesAndTerminates verifies that a string is copied
// into a C character array as strtcpy copies it: cut one byte short of the
// array and at an inner NUL, always terminated, the bytes past the
// terminator untouched.
func Test_CopyString_TruncatesAndTerminates(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  []byte
	}{
		{"short string keeps the bytes past the terminator", "ab", []byte("ab\x00xxx")},
		{"string one byte short of the array fills it", "abcde", []byte("abcde\x00")},
		{"string as long as the array is cut by one byte", "abcdef", []byte("abcde\x00")},
		{"longer string is cut to the array", "abcdefghij", []byte("abcde\x00")},
		{"inner terminator ends the copy", "a\x00bc", []byte("a\x00xxxx")},
		{"empty string writes only the terminator", "", []byte("\x00xxxxx")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field := []byte("xxxxxx")

			shm.CopyString(field, tc.value)

			require.Equal(t, tc.want, field)
		})
	}
}

// Test_CopyString_EmptyArray verifies that copying into an array with no
// room for a terminator returns instead of writing past it, as strtcpy
// refuses a zero size.
func Test_CopyString_EmptyArray(t *testing.T) {
	require.NotPanics(t, func() { shm.CopyString([]byte{}, "abc") })
}
