package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDepfileTargetPrefersEmitC pins the target meson declares as first output.
func TestDepfileTargetPrefersEmitC(t *testing.T) {
	cfg := config{ManifestOut: "abi_manifest.txt", EmitC: "abi_manifest_dataplane.c"}
	require.Equal(t, "abi_manifest_dataplane.c", depfileTarget(cfg))
}

// TestDepfileTargetFallsBackToManifestOut pins the fallback when -emit-c is unset.
func TestDepfileTargetFallsBackToManifestOut(t *testing.T) {
	cfg := config{ManifestOut: "abi_manifest.txt"}
	require.Equal(t, "abi_manifest.txt", depfileTarget(cfg))
}

// TestFormatDepfileEscapesSpaces pins make-style escaping of space in paths.
func TestFormatDepfileEscapesSpaces(t *testing.T) {
	out := formatDepfile("with space/out.c", []string{"with space/header.h"})
	require.Contains(t, out, "with\\ space/out.c")
	require.Contains(t, out, "with\\ space/header.h")
}

// TestParseMakeDepsRoundTripsEscapedSpaces pins the fix for split-on-space corruption.
func TestParseMakeDepsRoundTripsEscapedSpaces(t *testing.T) {
	deps := parseMakeDeps("out.o: with\\ space/header.h other.h\n")
	require.Equal(t, []string{"with space/header.h", "other.h"}, deps)
}
