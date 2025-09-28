package stage

// DataplaneInstanceIdx is the index of a dataplane instance.
type DataplaneInstanceIdx uint32

// Config represents a stage config in the configuration process.
//
// Each stage is a single configuration block that describes both modules with
// their configurations and the pipeline.
// A stage is applied atomically.
type Config struct {
	// Name is the unique identifier for this stage.
	Name string `yaml:"name"`
	// Configuration for each dataplane instance.
	Instances map[DataplaneInstanceIdx]DpInstanceConfig `yaml:"instance"`
}

// DpInstanceConfig contains the configuration for a specific dataplane instance.
type DpInstanceConfig struct {
	// Pipelines configuration for this dataplane instance.
	Pipelines []PipelineConfig `yaml:"pipelines,omitempty"`
	// Devices configuration for this dataplane instance.
	Devices []DeviceConfig `yaml:"devices,omitempty"`
}

// PipelineConfig represents a pipeline configuration.
type PipelineConfig struct {
	// Name is the unique identifier for this pipeline.
	Name string `yaml:"name"`
	// Chain define the processing chain in this pipeline.
	Functions []string `yaml:"chain"`
}

// DeviceConfig represents a device configuration.
type DeviceConfig struct {
	// ID is the ID of the device.
	Name string `yaml:"name"`
	DeviceId uint32 `yaml:"id"`
	Vlan uint32 `yaml:"vlan"`
	// Pipelines is the list of pipelines to assign to the device.
	Pipelines []DevicePipelineConfig `yaml:"pipelines"`
}

// DevicePipelineConfig represents a pipeline configuration for a device.
type DevicePipelineConfig struct {
	// Name is the name of the pipeline.
	Name string `yaml:"name"`
	// Weight is the weight of the pipeline.
	Weight uint64 `yaml:"weight"`
}
