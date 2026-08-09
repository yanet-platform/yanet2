package fwstate_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
)

const (
	deleteTestCPMemory = 64 * datasize.MB
	deleteTestDPMemory = 4 * datasize.MB
	deleteTestAgentMem = 16 * datasize.MB
	fwstateModuleType  = "fwstate"
)

func newDeleteTestHarness(
	testingTB testing.TB,
	modules []string,
	agentName string,
) (*dataplaneut.Harness, *ffi.Agent) {
	testingTB.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(deleteTestCPMemory),
		DPMemory:      uint64(deleteTestDPMemory),
		WorkerCount:   1,
		Modules:       modules,
		DevicesToLoad: []string{},
	})
	require.NoError(testingTB, err)
	testingTB.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		agentName,
		0,
		deleteTestAgentMem,
	)
	require.NoError(testingTB, err)
	testingTB.Cleanup(func() { _ = agent.CleanUp() })

	return harness, agent
}

func hasCPConfig(configs []ffi.CPConfig, moduleType, moduleName string) bool {
	for _, config := range configs {
		if config.Type == moduleType && config.Name == moduleName {
			return true
		}
	}
	return false
}

func newACLDeleteTestConfig(
	testingTB testing.TB,
	agent *ffi.Agent,
	name string,
) *cacl.ModuleConfig {
	testingTB.Helper()

	config, err := cacl.NewModuleConfig(agent, name)
	require.NoError(testingTB, err)
	testingTB.Cleanup(config.Free)
	require.NoError(testingTB, config.Update(nil, "", "", nil))
	require.NoError(testingTB, agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))

	return config
}

func TestFWStateDeleteKeepsSameNamedACLConfig(t *testing.T) {
	const configName = "shared-name"

	_, agent := newDeleteTestHarness(t, []string{"acl", "fwstate"}, "acl")
	newACLDeleteTestConfig(t, agent, configName)

	fwConfig, err := fwstate.NewFWStateModuleConfig(agent, configName)
	require.NoError(t, err)
	t.Cleanup(fwConfig.Free)
	require.NoError(t, cfwstate.SetModuleConfig(fwConfig.AsFFIModule(), "", "", cfwstate.SyncConfig{}))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{fwConfig.AsFFIModule()}))

	require.NoError(t, agent.DeleteModule(fwstateModuleType, configName))

	configs := agent.DPConfig().CPConfigs()
	require.True(t, hasCPConfig(configs, "acl", configName))
	require.False(t, hasCPConfig(configs, fwstateModuleType, configName))
}

func TestDeleteModuleConfigUsesRegisteredType(t *testing.T) {
	const configName = "fwstate-config"

	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-agent-instance")
	config, err := fwstate.NewFWStateModuleConfig(agent, configName)
	require.NoError(t, err)
	t.Cleanup(config.Free)
	require.NoError(t, cfwstate.SetModuleConfig(config.AsFFIModule(), "", "", cfwstate.SyncConfig{}))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))

	require.NoError(t, agent.DeleteModule(fwstateModuleType, configName))
	require.False(t, hasCPConfig(agent.DPConfig().CPConfigs(), fwstateModuleType, configName))
}
