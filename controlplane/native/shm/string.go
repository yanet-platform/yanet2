package shm

import (
	"bytes"
	"strings"
)

// CopyString copies value into a C character array as strtcpy does.
//
// It copies at most one byte short of the array, stopping at a NUL inside
// value, and writes a terminating NUL after it; the bytes past the
// terminator stay as they were. An empty array is left alone.
func CopyString(field []byte, value string) {
	if len(field) == 0 {
		return
	}
	if end := strings.IndexByte(value, 0); end >= 0 {
		value = value[:end]
	}
	length := min(len(value), len(field)-1)
	copy(field, value[:length])
	field[length] = 0
}

// String returns the NUL-terminated string a C character array holds.
func String(field []byte) string {
	return string(Bytes(field))
}

// Bytes returns the bytes a C character array holds before its
// terminating NUL, in place, so comparing or hashing them copies nothing.
func Bytes(field []byte) []byte {
	end := bytes.IndexByte(field, 0)
	if end < 0 {
		end = len(field)
	}
	return field[:end]
}

// Equal reports whether a C character array holds value, without copying
// the array.
func Equal(field []byte, value string) bool {
	return string(Bytes(field)) == value
}
