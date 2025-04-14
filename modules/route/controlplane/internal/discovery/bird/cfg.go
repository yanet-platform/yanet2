package bird

import (
	"fmt"
	"time"

	"github.com/c2h5oh/datasize"
	"gopkg.in/yaml.v3"
)

// To avoid infinite recursion, the validating wrapper casts itself to the
// private config struct. This allows the decoder to operate on it using the
// default behavior for handling Go structs without an unmarshal method.
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

// Socket struct holds the path to the socket providing a bird export feed,
// along with a human-readable descriptive name for this feed.
type Socket struct {
	// Name of the import provided by the bird socket.
	//
	// This name is used only for informational purposes as a human-readable
	// name for this kind of export.
	Name string `yaml:"name"`
	// Path to the Unix socket provided by the bird daemon.
	Path string `yaml:"path"`
}

// Config is a validating wrapper around the config struct
// (note the lowercase in the name).
type Config config
type config struct {
	// The Bird export protocol can be disabled. In this case, no additional
	// goroutines are spawned, and no background processing occurs, as if
	// there were no code for reading the Bird export protocol at all.
	Enable  bool     `yaml:"enable"`
	Sockets []Socket `yaml:"sockets"`
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
