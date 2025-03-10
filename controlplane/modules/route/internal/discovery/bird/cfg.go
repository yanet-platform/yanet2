package bird

import (
	"fmt"
	"time"

	"github.com/c2h5oh/datasize"
	"gopkg.in/yaml.v3"
)

func (m *Config) UnmarshalYAML(value *yaml.Node) error {
	err := value.Decode((*config)(m))
	if err != nil {
		return err
	}
	if m.Enable && len(m.Sockets) == 0 {
		return fmt.Errorf("bird export is enabled but no sockets are provided")
	}
	return nil
}

type Socket struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

type Config config
type config struct {
	Enable        bool              `yaml:"enable"`
	Sockets       []Socket          `yaml:"sockets"`
	ParserBufSize datasize.ByteSize `yaml:"parser_buf_size"`
	// DumpTimeout configures the timeout after which routes are forcibly dumped.
	DumpTimeout time.Duration `yaml:"dump_timeout"`
	// DumpThreshold configures the threshold beyond which routes are forcibly dumped.
	DumpThreshold int `yaml:"dump_threshold"`
}

func DefaultConfig() *Config {
	return &Config{
		ParserBufSize: datasize.MB,
		DumpTimeout:   time.Second,
		DumpThreshold: 1000,
	}
}
