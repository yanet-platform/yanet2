package stage

// NUMAIdx is the index of a NUMA node.
type NUMAIdx uint32

// Config represents a stage config in the configuration process.
//
// Each stage is a single configuration block that describes both modules with
// their configurations and the pipeline.
// A stage is applied atomically.
type Config struct {
	// Name is the unique identifier for this stage.
	Name string `yaml:"name"`
	// NUMA configuration for each node.
	NUMA map[NUMAIdx]NUMAConfig `yaml:"numa"`
}

// NUMAConfig contains the configuration for a specific NUMA node.
type NUMAConfig struct {
	// Modules configuration for this NUMA node.
	Modules map[string]ModuleConfig `yaml:"modules,omitempty"`
	// Pipeline configuration for this NUMA node.
	Pipeline *PipelineConfig `yaml:"pipeline,omitempty"`
}

// ModuleConfig contains configuration for a specific module.
type ModuleConfig struct {
	// ConfigName is the name of the configuration for this module.
	ConfigName string `yaml:"config_name"`
	// ConfigPath is the path to the module configuration file.
	ConfigPath string `yaml:"config_path"`
}

// PipelineConfig represents a pipeline configuration.
type PipelineConfig struct {
	// Name is the unique identifier for this pipeline.
	Name string `yaml:"name"`
	// Chain define the processing chain in this pipeline.
	Chain []NodeConfig `yaml:"chain"`
}

// NodeConfig represents a module node in a processing chain.
type NodeConfig struct {
	// ModuleName is the name of the module.
	ModuleName string `yaml:"module_name"`
	// ConfigName is the configuration name for this module.
	ConfigName string `yaml:"config_name"`
}
