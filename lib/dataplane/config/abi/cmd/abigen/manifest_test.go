package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTypeLineHashChangesWithAlignment pins that an aligned() attribute is hashed.
func TestTypeLineHashChangesWithAlignment(t *testing.T) {
	base := typeInfo{
		Kind: "struct", Name: "foo", ByteSize: 64,
		Members: []typeMember{{Name: "x", Type: "i4", ByteOffset: 0}},
	}

	aligned := base
	aligned.Alignment = 64

	require.NotEqual(t, entityHash(typeLine(base)), entityHash(typeLine(aligned)))
}

// TestAlignmentFieldDistinguishesAbsentFromExplicit pins the "natural" sentinel.
func TestAlignmentFieldDistinguishesAbsentFromExplicit(t *testing.T) {
	require.Equal(t, "natural", alignmentField(0))
	require.Equal(t, "64", alignmentField(64))
}
