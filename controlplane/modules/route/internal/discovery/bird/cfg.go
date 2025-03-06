package bird

import (
	"time"

	"github.com/c2h5oh/datasize"
)

type Socket struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

type Config struct {
	Enabled       bool
	Sockets       []Socket          `yaml:"sockets"`
	ParserBufSize datasize.ByteSize `yaml:"parser_buf_size"`
	// DumpTimeout configures the timeout after which routes are forcibly dumped.
	DumpTimeout time.Duration `yaml:"dump_timeout"`
	// DumpThreshold configures the threshold beyond which routes are forcibly dumped.
	DumpThreshold int `yaml:"dump_threshold"`
}

func DefaultConfig() *Config {
	return &Config{
		Enabled:       false,
		ParserBufSize: datasize.MB,
		DumpTimeout:   time.Second,
		DumpThreshold: 1000,
	}
}

// FIXME: validation
