package mock

import "github.com/c2h5oh/datasize"

type YanetMockDeviceConfig struct {
	Id   uint64
	Name string
}

type YanetMockConfig struct {
	CpMemory datasize.ByteSize
	DpMemory datasize.ByteSize
	Workers  uint64
	Devices  []YanetMockDeviceConfig
}
