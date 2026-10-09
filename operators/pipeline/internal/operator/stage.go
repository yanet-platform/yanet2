package operator

type StageConfig struct {
	Name      string           `yaml:"name"`
	Pipelines []PipelineConfig `yaml:"pipelines"`
	Devices   DevicesConfig    `yaml:"devices"`
}

type DevicesConfig struct {
	Plain []DeviceConfig      `yaml:"plain"`
	VLAN  []VLANDeviceConfig  `yaml:"vlan"`
	VXLAN []VXLANDeviceConfig `yaml:"vxlan"`
}

type PipelineConfig struct {
	Name      string   `yaml:"name"`
	Functions []string `yaml:"functions"`
}

type DeviceConfig struct {
	Name   string              `yaml:"name"`
	Input  []PipelineRefConfig `yaml:"input"`
	Output []PipelineRefConfig `yaml:"output"`
}

type VLANDeviceConfig struct {
	Name   string              `yaml:"name"`
	VLAN   uint32              `yaml:"vlan"`
	Input  []PipelineRefConfig `yaml:"input"`
	Output []PipelineRefConfig `yaml:"output"`
}

// VXLANDeviceConfig binds a vxlan device to its pipelines and its IPv4
// tunnel.
type VXLANDeviceConfig struct {
	Name   string              `yaml:"name"`
	Tunnel VXLANTunnelConfig   `yaml:"tunnel"`
	Input  []PipelineRefConfig `yaml:"input"`
	Output []PipelineRefConfig `yaml:"output"`
}

// VXLANTunnelConfig is the tunnel of a vxlan device.
//
// Addresses are written in their usual text forms: dotted-quad IPv4 and
// colon-separated EUI-48.
type VXLANTunnelConfig struct {
	LocalIP   string `yaml:"local_ip"`
	RemoteIP  string `yaml:"remote_ip"`
	LocalMAC  string `yaml:"local_mac"`
	RemoteMAC string `yaml:"remote_mac"`
	VNI       uint32 `yaml:"vni"`
}

type PipelineRefConfig struct {
	Name   string `yaml:"name"`
	Weight uint64 `yaml:"weight"`
}
