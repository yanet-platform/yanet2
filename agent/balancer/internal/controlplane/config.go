package controlplane

type Config struct {
	Endpoint      string `yaml:"endpoint"`
	InstanceCount uint32 `yaml:"instance_count"`
	ModuleName    string `yaml:"module_name"`
}
