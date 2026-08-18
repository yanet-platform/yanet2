package main

import (
	"debug/dwarf"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDescribeTypeRejectsUnrecognizedKind pins rejecting an unrecognized kind.
func TestDescribeTypeRejectsUnrecognizedKind(t *testing.T) {
	unrecognized := &dwarf.UnsupportedType{CommonType: dwarf.CommonType{Name: "weird"}, Tag: dwarf.TagRestrictType}
	_, err := describeType(unrecognized)
	require.Error(t, err, "should reject a DWARF type kind it does not recognize instead of encoding a compiler-dependent spelling")
}
