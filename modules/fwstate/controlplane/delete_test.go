package fwstate_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

const (
	deleteTestCPMemory = 64 * datasize.MB
	deleteTestDPMemory = 4 * datasize.MB
	deleteTestAgentMem = 16 * datasize.MB
	fwstateModuleType  = "fwstate"
)

type deleteTestACLProvider struct {
	aclConfig ffi.ModuleConfig
}

func (m deleteTestACLProvider) LinkedConfigNames(string) []string {
	return nil
}

func (m deleteTestACLProvider) RelinkConfigs(
	_ *fwstate.FwStateConfig,
	publish func([]ffi.ModuleConfig) error,
) error {
	return publish([]ffi.ModuleConfig{m.aclConfig})
}

func (m deleteTestACLProvider) LinkConfigs(
	_ []string,
	_ *fwstate.FwStateConfig,
	publish func([]ffi.ModuleConfig) error,
) error {
	return publish([]ffi.ModuleConfig{m.aclConfig})
}

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
	require.NoError(testingTB, config.UpdateRules(nil))

	return config
}

func TestFWStateDeleteKeepsSameNamedACLConfig(t *testing.T) {
	const configName = "shared-name"

	_, agent := newDeleteTestHarness(t, []string{"acl", "fwstate"}, "acl")
	aclConfig := newACLDeleteTestConfig(t, agent, configName)

	service := fwstate.NewFWStateService(agent, deleteTestACLProvider{
		aclConfig: aclConfig.AsFFIModule(),
	})
	_, err := service.UpdateConfig(t.Context(), validDeleteTestUpdateRequest(configName))
	require.NoError(t, err)

	_, err = service.DeleteConfig(t.Context(), &fwstatepb.DeleteConfigRequest{
		Name: configName,
	})
	require.NoError(t, err)

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
	require.NoError(t, config.CreateMaps(&fwstatepb.MapConfig{
		IndexSize:        1024,
		ExtraBucketCount: 64,
	}, 1))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))

	require.NoError(t, agent.DeleteModuleConfig(fwstateModuleType, configName))
	require.False(t, hasCPConfig(agent.DPConfig().CPConfigs(), fwstateModuleType, configName))
}

func validDeleteTestUpdateRequest(name string) *fwstatepb.UpdateConfigRequest {
	return &fwstatepb.UpdateConfigRequest{
		Name: name,
		SyncConfig: &fwstatepb.SyncConfig{
			SrcAddr:          &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
			DstEther:         commonpb.NewMACAddressEUI48([6]byte{1, 2, 3, 4, 5, 6}),
			DstAddrMulticast: &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
			PortMulticast:    9999,
		},
		MapConfig: &fwstatepb.MapConfig{
			IndexSize:        1024,
			ExtraBucketCount: 64,
		},
	}
}
