package coordinator

import (
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/bird"
)

// Config defines the configuration for the route coordinator module.
type Config struct {
	// Routes maps route prefixes to next hop addresses.
	Routes []Route `yaml:"routes"`
	// BirdImport configures the reader for the Bird Export Protocol feed.
	BirdImport *bird.Config `yaml:"bird_import"`
}

type Route struct {
	Prefix  netip.Prefix `yaml:"prefix"`
	Nexthop netip.Addr   `yaml:"nexthop"`
}

func DefaultConfig() *Config {
	return &Config{
		BirdImport: bird.DefaultConfig(),
	}
}
