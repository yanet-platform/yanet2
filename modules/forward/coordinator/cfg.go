package coordinator

import (
	"net/netip"
)

// Config defines the configuration for a forward module.
type Config struct {
	// L2Forwards contains forwarding rules for devices.
	L2Forwards []L2Forward `yaml:"l2_forwards"`
	// L3Forwards contains forwarding rules for devices.
	L3Forwards []L3Forward `yaml:"l3_forwards"`
}

// L2Forward defines the L2 forwarding configuration.
type L2Forward struct {
	// SourceDeviceID is the source device ID.
	SourceDeviceID uint16 `yaml:"source_device_id"`
	// DestinationDeviceID is the target device ID.
	DestinationDeviceID uint16 `yaml:"destination_device_id"`
}

// L3Forward defines the L3 forwarding configuration.
type L3Forward struct {
	// SourceDeviceID is the source device ID.
	SourceDeviceID uint16 `yaml:"source_device_id"`
	// Rules contains forwarding rules.
	Rules []ForwardRule `yaml:"rules"`
}

// ForwardRule defines a forwarding rule for both IPv4 and IPv6.
type ForwardRule struct {
	// Network is the network prefix for forwarding.
	Network netip.Prefix `yaml:"network"`
	// DestinationDeviceID is the target device ID.
	DestinationDeviceID uint16 `yaml:"destination_device_id"`
}
