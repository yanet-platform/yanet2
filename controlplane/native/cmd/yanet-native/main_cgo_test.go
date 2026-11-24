//go:build linux && (amd64 || arm64) && cgo

package main

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	// Links the dataplane module types, which the C bootstrap of a
	// storage loads from this binary.
	_ "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ffi/ffitest"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// publishedStorage returns a storage file of two instances the C bootstrap
// made, with blackhole module types loaded, and on the first one an idle
// worker and two blackhole configurations an agent published.
func publishedStorage(t *testing.T) string {
	t.Helper()
	path := ffitest.NewStorage(t, ffitest.StorageTypes{Modules: []string{"blackhole"}, InstanceCount: 2})
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })
	ffitest.PrepareWorker(t, memory, 0, 1_700_000_000_000_000_005)
	agent, err := memory.AgentAttach("native-demo", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })
	var modules []ffi.ModuleConfig
	for _, name := range []string{"first", "second"} {
		config, err := cblackhole.NewModuleConfig(agent, name)
		require.NoError(t, err)
		t.Cleanup(func() { _ = config.Free() })
		modules = append(modules, config.AsFFIModule())
	}
	require.NoError(t, agent.UpdateModules(modules))
	// An agent records the generation published when it attaches, which
	// is how the cgo readers tell the current one.
	for idx := range uint32(2) {
		observer, err := memory.AgentAttach("observer", idx, 4*datasize.MB)
		require.NoError(t, err)
		t.Cleanup(func() { _ = observer.CleanUp() })
	}
	return path
}

// Test_Inspector_ReportsWhatCgoReads verifies that the command, run as a
// separate process on a storage the C bootstrap made, reports the same
// instances, module types, published generation and modules, agents,
// counters and dataplane time as the cgo control plane reads.
func Test_Inspector_ReportsWhatCgoReads(t *testing.T) {
	path := publishedStorage(t)

	output, err := exec.Command(binary, "-storage", path).Output()
	require.NoError(t, err)
	var report Report
	require.NoError(t, json.Unmarshal(output, &report))

	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })

	require.Len(t, report.Instances, 2)
	require.Len(t, report.Instances[0].Modules, 2)
	for idx, instance := range report.Instances {
		cgo := memory.DPConfig(uint32(idx))
		t.Run("instance "+strconv.Itoa(idx), func(t *testing.T) {
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

			var modules []zone.Module
			for _, config := range cgo.CPConfigs() {
				modules = append(modules, zone.Module{
					Type: config.Type, Name: config.Name, Agent: "native-demo", Generation: config.Gen,
				})
			}
			require.ElementsMatch(t, modules, instance.Modules)

			var agents []string
			for _, agent := range cgo.Agents() {
				agents = append(agents, agent.Name)
				if agent.Name == "observer" {
					require.Equal(t, agent.Instances[0].Gen, instance.Generation)
				}
			}
			var gotAgents []string
			for _, agent := range instance.Agents {
				gotAgents = append(gotAgents, agent.Name)
			}
			require.ElementsMatch(t, agents, gotAgents)

			groups, err := cgo.CountersByTags(nil, nil)
			require.NoError(t, err)
			counters := []zone.CounterGroup{}
			for _, group := range groups {
				converted := zone.CounterGroup{}
				for _, tag := range group.Tags {
					converted.Tags = append(converted.Tags, zone.CounterTag{Key: tag.Key, Value: tag.Value})
				}
				for _, counter := range group.Counters {
					converted.Counters = append(converted.Counters, zone.Counter{Name: counter.Name, Values: counter.Values})
				}
				counters = append(counters, converted)
			}
			require.Equal(t, counters, instance.Counters)

			now, ok := cgo.CurrentTime()
			require.Equal(t, ok, instance.CurrentTime != nil)
			if ok {
				require.Equal(t, now, *instance.CurrentTime)
			}
		})
	}
}

// Test_Inspector_PrintsEmptyLists verifies that lists with nothing in
// them print as empty JSON arrays, not as null.
func Test_Inspector_PrintsEmptyLists(t *testing.T) {
	output, err := exec.Command(binary, "-storage", publishedStorage(t)).Output()
	require.NoError(t, err)
	var report map[string][]map[string]any
	require.NoError(t, json.Unmarshal(output, &report))
	second := report["instances"][1]
	for _, key := range []string{"workers", "modules", "counters"} {
		require.Equal(t, []any{}, second[key], key)
	}
}
