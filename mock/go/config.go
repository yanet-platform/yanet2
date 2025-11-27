package mock

type YanetMockDeviceConfig struct {
	Id   uint64
	Name string
}

type YanetMockConfig struct {
	CpMemory uint64
	DpMemory uint64
	Workers  uint64
	Devices  []YanetMockDeviceConfig
}
