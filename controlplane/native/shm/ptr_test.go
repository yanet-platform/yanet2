package shm_test

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// node is a block of shared memory holding a relative pointer and a value
// it may point at.
type node struct {
	next  shm.Ptr[uint64]
	value uint64
}

// storedOffset returns the distance a relative pointer stores, as C reads
// it.
func storedOffset(pointer *shm.Ptr[uint64]) int64 {
	return *(*int64)(unsafe.Pointer(pointer))
}

// Test_Ptr_SetAndGet verifies that a pointer set at a target resolves to
// it from its own address, forwards and backwards.
func Test_Ptr_SetAndGet(t *testing.T) {
	var nodes [2]node
	cases := []struct {
		name    string
		pointer *shm.Ptr[uint64]
		target  *uint64
		offset  int64
	}{
		{"forward", &nodes[0].next, &nodes[1].value, int64(unsafe.Sizeof(node{})) + 8},
		{"backward", &nodes[1].next, &nodes[0].value, 8 - int64(unsafe.Sizeof(node{}))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.pointer.Set(tc.target)

			require.Same(t, tc.target, tc.pointer.Get())
			require.Equal(t, tc.offset, storedOffset(tc.pointer))
		})
	}
}

// Test_Ptr_StoreAndLoad verifies that the ordered accessors store and
// resolve the same distance as the plain ones.
func Test_Ptr_StoreAndLoad(t *testing.T) {
	var nodes [2]node

	nodes[1].next.Store(&nodes[0].value)

	require.Same(t, &nodes[0].value, nodes[1].next.Load())
	require.Equal(t, 8-int64(unsafe.Sizeof(node{})), storedOffset(&nodes[1].next))
}

// Test_Ptr_Nil verifies that nil is stored as a zero distance and that a
// zero distance resolves to nil, through every accessor.
func Test_Ptr_Nil(t *testing.T) {
	cases := []struct {
		name  string
		clear func(pointer *shm.Ptr[uint64])
		read  func(pointer *shm.Ptr[uint64]) *uint64
	}{
		{"set and get", func(pointer *shm.Ptr[uint64]) { pointer.Set(nil) }, (*shm.Ptr[uint64]).Get},
		{"store and load", func(pointer *shm.Ptr[uint64]) { pointer.Store(nil) }, (*shm.Ptr[uint64]).Load},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var block node
			block.next.Set(&block.value)
			require.False(t, block.next.IsNil())

			tc.clear(&block.next)

			require.True(t, block.next.IsNil())
			require.Zero(t, storedOffset(&block.next))
			require.Nil(t, tc.read(&block.next))
		})
	}
}

// Test_Ptr_Size verifies that the pointer types take the 8 bytes of the C
// pointers they stand for.
func Test_Ptr_Size(t *testing.T) {
	cases := []struct {
		name string
		size uintptr
	}{
		{"relative pointer", unsafe.Sizeof(shm.Ptr[uint64]{})},
		{"process-local word", unsafe.Sizeof(shm.Local(0))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, uintptr(8), tc.size)
		})
	}
}

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
// room for a terminator writes nothing, as strtcpy refuses a zero size.
func Test_CopyString_EmptyArray(t *testing.T) {
	require.NotPanics(t, func() { shm.CopyString([]byte{}, "abc") })
}
