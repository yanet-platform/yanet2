package core

import (
	"fmt"
	"net/netip"

	"github.com/yanet-platform/monalive/pkg/keepalived"
)

type Config struct {
	// List of virtual servers configurations.
	Services []Service `keepalive:"virtual_server"`
}

// LoadConfig loads and validates a configuration from the specified file path.
func LoadConfig(path string) (*Config, error) {
	cfg := new(Config)
	err := keepalived.LoadConfig(path, cfg)
	if err != nil {
		return nil, err
	}
	err = cfg.validate()
	if err != nil {
		return nil, err
	}
	cfg.propagate()
	return cfg, nil
}

func (cfg *Config) validate() error {
	for _, s := range cfg.Services {
		if s.IPv4OuterSourceNetwork.IsValid() {
			if !s.IPv4OuterSourceNetwork.Addr().Is4() {
				return fmt.Errorf("not a ipv4 addr in ipv4_outer_source_network")
			}
		}
		if s.IPv6OuterSourceNetwork.IsValid() {
			if !s.IPv6OuterSourceNetwork.Addr().Is6() {
				return fmt.Errorf("not a ipv6 addr in ipv6_outer_source_network")
			}
		}
	}
	return nil
}

func (cfg *Config) propagate() {
	for _, s := range cfg.Services {
		for _, r := range s.Reals {
			if r.ForwardingMethod == "" {
				r.ForwardingMethod = s.ForwardingMethod
			}
		}
	}
}

type Service struct {
	VIP netip.Addr `keepalive_pos:"0"`
	// Forwarding method to send health checks to the service.
	ForwardingMethod ForwardingMethod `keepalive:"lvs_method" default:"TUN"`
	// Outer source network for IPv4.
	IPv4OuterSourceNetwork netip.Prefix `keepalive:"ipv4_outer_source_network"`
	// Outer source network for IPv6.
	IPv6OuterSourceNetwork netip.Prefix `keepalive:"ipv6_outer_source_network"`
	// List of real server configurations.
	Reals []Real `keepalive:"real_server"`
}

// Equals implements deep equal check.
func (s Service) Equals(s2 Service) bool {
	if s.VIP != s2.VIP ||
		s.ForwardingMethod != s2.ForwardingMethod ||
		s.IPv4OuterSourceNetwork != s2.IPv4OuterSourceNetwork ||
		s.IPv6OuterSourceNetwork != s2.IPv6OuterSourceNetwork {
		return false
	}
	if len(s.Reals) != len(s2.Reals) {
		return false
	}
	for i := range s.Reals {
		if s.Reals[i] != s2.Reals[i] {
			return false
		}
	}
	return true
}

type Real struct {
	// IP address of the real server.
	IP netip.Addr `keepalive_pos:"0"`
	// Weight for of the real.
	Weight uint16 `keepalive:"weight" default:"1"`
	// Forwarding method to send health checks to the service.
	ForwardingMethod ForwardingMethod `keepalive:"lvs_method"` // optional
}

type ForwardingMethod string

const (
	TUN ForwardingMethod = "TUN"
	GRE ForwardingMethod = "GRE"
)

// UnmarshalText is a part of [encoding.TextUnmarshaler]
func (fm *ForwardingMethod) UnmarshalText(text []byte) error {
	switch method := ForwardingMethod(text); method {
	case TUN, GRE, "":
		*fm = method
		return nil
	}
	return fmt.Errorf("unknown lvs_method")
}
