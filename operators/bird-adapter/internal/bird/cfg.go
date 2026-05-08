package bird

import (
	"time"

	"github.com/c2h5oh/datasize"
)

type Config struct {
	// Paths to the Unix sockets provided by the bird daemon.
	Sockets []string `yaml:"sockets"`
	// Buffer size for the parser. It should be large enough to hold at least one update message.
	// Every goroutine spawned for each socket will allocate its own buffer.
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
		DumpThreshold: 10_000,
	}
}
