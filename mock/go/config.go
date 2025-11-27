package mock

type YanetMockDeviceConfig struct {
	id   uint64
	name string
}

type YanetMockConfig struct {
	CpMemory uint64
	DpMemory uint64
	Workers  uint64
	Devices  []YanetMockDeviceConfig
}
