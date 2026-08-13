package fwstate_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
	objfwstate "github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
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
		ObjectsToLoad: []string{"fwstate_map_v4", "fwstate_map_v6"},
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

// fwstateTestMaps owns a published pair of fwstate-map objects a module
// config links by name.
type fwstateTestMaps struct {
	name string
	v4   *objfwstate.MapObjectConfig
	v6   *objfwstate.MapObjectConfig
}

func (m fwstateTestMaps) v4Name() string { return m.name + "-v4" }
func (m fwstateTestMaps) v6Name() string { return m.name + "-v6" }

// newFWStateTestMaps publishes one v4 and one v6 map object pair under
// name+"-v4"/name+"-v6" with the given first-layer index size.
func newFWStateTestMaps(
	testingTB testing.TB,
	agent *ffi.Agent,
	name string,
	indexSize uint32,
) fwstateTestMaps {
	testingTB.Helper()

	newMap := func(kind objfwstate.Kind) *objfwstate.MapObjectConfig {
		testingTB.Helper()

		mapObject, err := objfwstate.NewMapObjectConfig(agent, name+"-"+kind.String(), kind)
		require.NoError(testingTB, err)
		require.NoError(testingTB, mapObject.CreateMap(indexSize, 64, 1))
		require.NoError(testingTB, mapObject.Publish(agent))
		testingTB.Cleanup(mapObject.Free)
		return mapObject
	}

	return fwstateTestMaps{
		name: name,
		v4:   newMap(objfwstate.KindV4),
		v6:   newMap(objfwstate.KindV6),
	}
}

func newACLDeleteTestConfig(
	testingTB testing.TB,
	agent *ffi.Agent,
	name string,
) *cacl.ModuleConfig {
	testingTB.Helper()

	config, err := cacl.NewModuleConfig(agent, name, nil, "", "", nil)
	require.NoError(testingTB, err)
	testingTB.Cleanup(config.Free)

	return config
}

func TestFWStateDeleteKeepsSameNamedACLConfig(t *testing.T) {
	const configName = "shared-name"

	_, agent := newDeleteTestHarness(t, []string{"acl", "fwstate"}, "acl")
	aclConfig := newACLDeleteTestConfig(t, agent, configName)
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{aclConfig.AsFFIModule()}))
	maps := newFWStateTestMaps(t, agent, configName, 1024)

	service := fwstate.NewFWStateService(agent)
	_, err := service.UpdateConfig(t.Context(), validDeleteTestUpdateRequest(
		configName, maps.v4Name(), maps.v6Name(),
	))
	require.NoError(t, err)

	_, err = service.DeleteConfig(t.Context(), &fwstatepb.DeleteConfigRequest{
		Name: configName,
	})
	require.NoError(t, err)

	configs := agent.DPConfig().CPConfigs()
	require.True(t, hasCPConfig(configs, "acl", configName))
	require.False(t, hasCPConfig(configs, fwstateModuleType, configName))
}

// TestFWStateUpdateUnknownMapNameRejected checks that an update naming a
// map object that is not published fails with InvalidArgument carrying the
// C-side generation-install error naming the object, and that nothing is
// published by the failed update.
func TestFWStateUpdateUnknownMapNameRejected(t *testing.T) {
	const configName = "fwstate-unknown-map"

	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-unknown-map")
	maps := newFWStateTestMaps(t, agent, "maps", 1024)
	service := fwstate.NewFWStateService(agent)

	request := validDeleteTestUpdateRequest(configName, maps.v4Name(), maps.v6Name())
	request.MapNameV6 = "no-such-map"

	_, err := service.UpdateConfig(t.Context(), request)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, err.Error(), "no-such-map")
	require.Contains(t, err.Error(), "linked object")

	_, err = service.ShowConfig(t.Context(), &fwstatepb.ShowConfigRequest{Name: configName})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestDeleteModuleConfigUsesRegisteredType(t *testing.T) {
	const configName = "fwstate-config"

	_, agent := newDeleteTestHarness(t, []string{"fwstate"}, "fwstate-agent-instance")
	maps := newFWStateTestMaps(t, agent, configName, 1024)
	config, err := fwstate.NewFWStateModuleConfig(
		agent,
		configName,
		nil,
		validDeleteTestUpdateRequest(configName, maps.v4Name(), maps.v6Name()).SyncConfig,
		maps.v4Name(),
		maps.v6Name(),
	)
	require.NoError(t, err)
	t.Cleanup(config.Free)
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))

	require.NoError(t, agent.DeleteModuleConfig(fwstateModuleType, configName))
	require.False(t, hasCPConfig(agent.DPConfig().CPConfigs(), fwstateModuleType, configName))
}

func validDeleteTestUpdateRequest(name, fw4MapName, fw6MapName string) *fwstatepb.UpdateConfigRequest {
	return &fwstatepb.UpdateConfigRequest{
		Name:      name,
		MapNameV4: fw4MapName,
		MapNameV6: fw6MapName,
		SyncConfig: &fwstatepb.SyncConfig{
			SrcAddr:          &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
			DstAddrMulticast: &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}},
			PortMulticast:    9999,
		},
	}
}
