package operator

type StageConfig struct {
	Name      string           `yaml:"name"`
	Pipelines []PipelineConfig `yaml:"pipelines"`
	Devices   DevicesConfig    `yaml:"devices"`
}

type DevicesConfig struct {
	Plain []DeviceConfig     `yaml:"plain"`
	VLAN  []VLANDeviceConfig `yaml:"vlan"`
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

type PipelineRefConfig struct {
	Name   string `yaml:"name"`
	Weight uint64 `yaml:"weight"`
}
