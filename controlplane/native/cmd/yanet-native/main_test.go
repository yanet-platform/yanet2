//go:build linux && (amd64 || arm64)

package main

import (
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// binary is the path of yanet-native built once without cgo for every
// test.
var binary string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests builds the command without cgo into a temporary directory,
// runs the tests and removes the directory.
func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "yanet-native")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	binary = filepath.Join(dir, "yanet-native")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build yanet-native: %v\n%s", err, output)
		return 1
	}
	return m.Run()
}

// Test_Inspector_IsStatic verifies that the command builds without cgo
// into an executable with no dynamic loader and no shared libraries.
func Test_Inspector_IsStatic(t *testing.T) {
	file, err := elf.Open(binary)
	require.NoError(t, err)
	defer file.Close()
	for _, program := range file.Progs {
		require.NotEqual(t, elf.PT_INTERP, program.Type, "the executable asks for a dynamic loader")
	}
	libraries, err := file.ImportedLibraries()
	require.NoError(t, err)
	require.Empty(t, libraries)
}
