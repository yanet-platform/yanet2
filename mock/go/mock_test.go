package mock

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

////////////////////////////////////////////////////////////////////////////////

func TestBasic(t *testing.T) {
	config := YanetMockConfig{
		CpMemory: 1 << 27,
		DpMemory: 1 << 20,
		Workers:  1,
		Devices: []YanetMockDeviceConfig{
			{
				Id:   0,
				Name: "01:00.0",
			},
		},
	}
	mock, err := NewYanetMock(&config)
	require.NoError(t, err)

	defer mock.Free()

	shm := mock.SharedMemory()
	agent, err := shm.AgentAttach("config", 0, 1<<20)
	require.NoError(t, err)
	require.NotNil(t, agent)

	{
		functionConfig := ffi.FunctionConfig{
			Name: "test",
			Chains: []ffi.FunctionChainConfig{
				{
					Weight: 1,
					Chain: ffi.ChainConfig{
						Name:    "ch0",
						Modules: []ffi.ChainModuleConfig{},
					},
				},
			},
		}

		err = agent.UpdateFunction(functionConfig)
		assert.NoError(t, err)
	}

	// update pipelines
	{
		pipelineConfig := ffi.PipelineConfig{
			Name:      "test",
			Functions: []string{"test"},
		}

		err = agent.UpdatePipeline(pipelineConfig)
		assert.NoError(t, err)
	}

	{
		pipelineConfig := ffi.PipelineConfig{
			Name:      "dummy",
			Functions: []string{},
		}

		err = agent.UpdatePipeline(pipelineConfig)
		assert.NoError(t, err)
	}

	// update devices
	{
		deviceConfig := ffi.DeviceConfig{
			Name: "01:00.0",
			Input: []ffi.DevicePipelineConfig{
				{
					Name:   "test",
					Weight: 1,
				},
			},
			Output: []ffi.DevicePipelineConfig{
				{
					Name:   "dummy",
					Weight: 1,
				},
			},
		}

		err = agent.UpdatePlainDevices([]ffi.DeviceConfig{deviceConfig})
		assert.NoError(t, err)
	}
}
