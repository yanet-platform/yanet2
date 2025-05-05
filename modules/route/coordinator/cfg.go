package coordinator

import (
	"net/netip"
)

// Config defines the configuration for the route coordinator module.
type Config struct {
	// Routes maps route prefixes to next hop addresses.
	Routes []Route `yaml:"routes"`
}

type Route struct {
	Prefix  netip.Prefix `yaml:"prefix"`
	Nexthop netip.Addr   `yaml:"nexthop"`
}
