package mock

import (
	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/common/go/testutils"
)

type YanetMockDeviceConfig struct {
	Id   uint64
	Name string
}

type YanetMockConfig struct {
	AgentsMemory datasize.ByteSize
	DpMemory     datasize.ByteSize
	Workers      uint64
	Devices      []YanetMockDeviceConfig
}

func (m *YanetMockConfig) GetDpMemory() datasize.ByteSize {
	if m.DpMemory == 0 {
		return datasize.MB * 32
	}
	return m.DpMemory
}

func (m *YanetMockConfig) GetCpMemory() datasize.ByteSize {
	cpInternalMemoryRequirements := datasize.MB*2 + testutils.CPAlignmentOverhead()
	return cpInternalMemoryRequirements + m.GetAgentsMemory()
}

func (m *YanetMockConfig) GetAgentsMemory() datasize.ByteSize {
	return m.AgentsMemory + testutils.CPAlignmentOverhead()
}
