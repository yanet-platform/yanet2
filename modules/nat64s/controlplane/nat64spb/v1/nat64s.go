package nat64spb

import (
	"errors"
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// The smallest MTU limit the translator works with, in bytes: every IPv4
// link must take a 68-byte datagram (RFC 791) and every IPv6 link a
// 1280-byte packet (RFC 8200).
const (
	minIPv4MTU = 68
	minIPv6MTU = 1280
)

// Validate checks that a show request names a configuration.
func (m *ShowConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

// Validate checks the values an update carries before anything is built
// from them.
func (m *UpdateConfigRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}
	if m.GetConfig() == nil {
		return errors.New("config is required")
	}
	if err := m.GetConfig().Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	return nil
}

// Validate checks that a delete request names a configuration.
func (m *DeleteConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

// Validate checks the limits only the request can judge; the rest is
// judged when the config is decoded into its stored form.
func (m *Config) Validate() error {
	if err := m.GetMtu().Validate(); err != nil {
		return fmt.Errorf("mtu: %w", err)
	}

	return nil
}

// Validate checks that each MTU limit is zero, which leaves it unset, or
// at least its protocol's minimum.
func (m *MTUConfig) Validate() error {
	if mtu := m.GetIpv4Mtu(); mtu != 0 && mtu < minIPv4MTU {
		return fmt.Errorf(
			"ipv4_mtu %d is below the IPv4 minimum of %d",
			mtu,
			minIPv4MTU,
		)
	}
	if mtu := m.GetIpv6Mtu(); mtu != 0 && mtu < minIPv6MTU {
		return fmt.Errorf(
			"ipv6_mtu %d is below the IPv6 minimum of %d",
			mtu,
			minIPv6MTU,
		)
	}

	return nil
}
