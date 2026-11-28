//go:build linux && (amd64 || arm64)

package main

import (
	"debug/elf"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
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

// keptDataplane returns a dataplane of the configuration whose storage is
// a file kept for the test, and the file's path.
func keptDataplane(t *testing.T, config nativetest.Config) (*nativetest.Dataplane, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(nativetest.KeepEnv, dir)
	dataplane := nativetest.NewDataplane(t, config)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	return dataplane, filepath.Join(dir, entries[0].Name())
}

// inspect runs the command on the storage file and returns its output.
func inspect(t *testing.T, path string) []byte {
	t.Helper()
	output, err := exec.Command(binary, "-storage", path).Output()
	require.NoError(t, err)
	return output
}

// Test_Inspector_ReportsPublishedTopology verifies that the command, run
// as a separate process on a dataplane's storage, reports the instance,
// module and object types, published generation, modules and objects,
// agents, counters and dataplane time of the topology an agent published.
func Test_Inspector_ReportsPublishedTopology(t *testing.T) {
	dataplane, path := keptDataplane(t, nativetest.Config{
		Workers:     2,
		Devices:     []string{"port0"},
		Modules:     []string{"blackhole"},
		DeviceTypes: []string{"plain"},
		ObjectTypes: []string{"ring"},
	})
	instance := dataplane.Instance()
	owner, err := agent.Attach(instance, "native-demo", uint64(16*datasize.MB))
	require.NoError(t, err)
	deliver := cpconfig.WithDelivery(dataplane.Deliver)
	var modules []*layout.CpModule
	for _, name := range []string{"first", "second"} {
		module, err := cpconfig.NewBlackhole(owner, name)
		require.NoError(t, err)
		modules = append(modules, module)
	}
	require.NoError(t, cpconfig.UpdateModules(instance, modules, deliver))
	require.NoError(t, cpconfig.UpdatePipelines(instance, []cpconfig.PipelineConfig{{Name: "input"}, {Name: "output"}}, deliver))
	device, err := cpconfig.NewPlainDevice(owner, cpconfig.DeviceConfig{
		Name:   "port0",
		Input:  []cpconfig.DevicePipelineConfig{{Name: "input", Weight: 1}},
		Output: []cpconfig.DevicePipelineConfig{{Name: "output", Weight: 1}},
	})
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateDevices(instance, []*layout.CpDevice{device}, deliver))
	ring := (*layout.CpObject)(shmalloc.ContextAlloc(&owner.Agent.MemoryContext, unsafe.Sizeof(layout.CpObject{})))
	require.NotNil(t, ring)
	require.NoError(t, cpconfig.InitObject(ring, owner, "ring", "events"))
	require.NoError(t, cpconfig.UpdateObjects(instance, []*layout.CpObject{ring}, deliver))
	now := time.Unix(1_700_000_000, 5)
	require.NoError(t, dataplane.SetTime(now))
	generation := instance.Cp.CpConfigGen.Get().Gen
	pinned, err := instance.Pin()
	require.NoError(t, err)
	counters, err := pinned.Counters(nil)
	pinned.Release()
	require.NoError(t, err)

	var report Report
	require.NoError(t, json.Unmarshal(inspect(t, path), &report))

	require.Len(t, report.Instances, 1)
	got := report.Instances[0]
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"numa node", func(t *testing.T) {
			// The harness runs its one instance on the first node.
			require.Zero(t, got.NumaNode)
		}},
		{"workers", func(t *testing.T) {
			require.Len(t, got.Workers, 2)
		}},
		{"dataplane module types", func(t *testing.T) {
			var types []string
			for _, module := range got.DataplaneModules {
				types = append(types, module.Name)
			}
			require.Equal(t, []string{"blackhole"}, types)
		}},
		{"dataplane object types", func(t *testing.T) {
			require.Equal(t, []zone.DataplaneObject{{Name: "ring"}}, got.DataplaneObjects)
		}},
		{"published generation", func(t *testing.T) {
			require.Equal(t, generation, got.Generation)
		}},
		{"published modules", func(t *testing.T) {
			require.ElementsMatch(t, []zone.Module{
				{Type: "blackhole", Name: "first", Agent: "native-demo"},
				{Type: "blackhole", Name: "second", Agent: "native-demo"},
			}, got.Modules)
		}},
		{"published objects", func(t *testing.T) {
			require.Equal(t, []zone.Object{{Type: "ring", Name: "events", Agent: "native-demo"}}, got.Objects)
		}},
		{"agents", func(t *testing.T) {
			var names []string
			for _, entry := range got.Agents {
				names = append(names, entry.Name)
			}
			require.Equal(t, []string{"native-demo"}, names)
		}},
		{"counters", func(t *testing.T) {
			// The device and its two pipelines count; the reader's own
			// tests pin each group.
			require.Len(t, got.Counters, 3)
			require.Equal(t, counters, got.Counters)
		}},
		{"dataplane time", func(t *testing.T) {
			require.NotNil(t, got.CurrentTime)
			require.Equal(t, uint64(now.UnixNano()), *got.CurrentTime)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.check)
	}
}

// Test_Inspector_PrintsEmptyLists verifies that lists with nothing in
// them print as empty JSON arrays, not as null: a dataplane that loads no
// module or object type and to which nothing attached or published.
func Test_Inspector_PrintsEmptyLists(t *testing.T) {
	_, path := keptDataplane(t, nativetest.Config{})

	var report map[string][]map[string]any
	require.NoError(t, json.Unmarshal(inspect(t, path), &report))

	require.Len(t, report["instances"], 1)
	instance := report["instances"][0]
	for _, key := range []string{"dataplane_modules", "dataplane_objects", "modules", "objects", "agents", "counters"} {
		require.Equal(t, []any{}, instance[key], key)
	}
}
