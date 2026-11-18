//go:build linux && amd64

package main

import (
	"debug/elf"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ffi/ffitest"
)

// buildStatic builds yanet-native with cgo disabled and returns its path.
func buildStatic(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "yanet-native")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	return binary
}

// Test_YanetNative_IsStatic verifies that the command builds without cgo
// into an executable with no dynamic loader and no shared libraries.
func Test_YanetNative_IsStatic(t *testing.T) {
	file, err := elf.Open(buildStatic(t))
	require.NoError(t, err)
	defer file.Close()
	for _, program := range file.Progs {
		require.NotEqual(t, elf.PT_INTERP, program.Type, "the executable asks for a dynamic loader")
	}
	libraries, err := file.ImportedLibraries()
	require.NoError(t, err)
	require.Empty(t, libraries)
}

// Test_YanetNative_ReportsWhatCgoReads verifies that the command, run as a
// separate process on a storage the C bootstrap made, reports the same
// instances, module types and agents as the cgo control plane reads.
func Test_YanetNative_ReportsWhatCgoReads(t *testing.T) {
	binary := buildStatic(t)
	path := ffitest.NewStorage(t, ffitest.StorageTypes{InstanceCount: 2})

	output, err := exec.Command(binary, "-storage", path).Output()
	require.NoError(t, err)
	var report Report
	require.NoError(t, json.Unmarshal(output, &report))

	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })

	require.Len(t, report.Instances, 2)
	for idx, instance := range report.Instances {
		cgo := memory.DPConfig(uint32(idx))
		t.Run("instance "+string(rune('0'+idx)), func(t *testing.T) {
			require.Equal(t, cgo.NumaIdx(), instance.NumaNode)
			require.Len(t, instance.Workers, int(cgo.WorkerCount()))

			var types []string
			for _, module := range cgo.Modules() {
				types = append(types, module.Name())
			}
			var got []string
			for _, module := range instance.DataplaneModules {
				got = append(got, module.Name)
			}
			require.Equal(t, types, got)

			var agents []string
			for _, agent := range cgo.Agents() {
				agents = append(agents, agent.Name)
			}
			var gotAgents []string
			for _, agent := range instance.Agents {
				gotAgents = append(gotAgents, agent.Name)
			}
			require.ElementsMatch(t, agents, gotAgents)
		})
	}
}
