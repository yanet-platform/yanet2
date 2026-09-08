package netplan

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

const (
	minIPv6MTU  = 1280
	maxLinuxMTU = 1<<31 - 1
)

// ValidateInterfaceName checks Linux naming limits and per-interface sysctl
// safety, including names reserved for host-wide policy.
func ValidateInterfaceName(name string) error {
	if name == "" {
		return errors.New("interface name is empty")
	}
	if len(name) > 15 {
		return errors.New("interface name exceeds Linux IFNAMSIZ")
	}
	if name == "." || name == ".." || name == "all" || name == "default" {
		return errors.New("interface name is reserved")
	}
	if strings.ContainsAny(name, "/:\x00 \t\n\v\f\r") {
		return errors.New("interface name contains a character rejected by Linux")
	}
	return nil
}

// ValidateMTU accepts an unspecified MTU or a Linux MTU that supports IPv6.
//
// Smaller MTUs disable IPv6 and can discard addresses owned by other managers.
func ValidateMTU(mtu int) error {
	if mtu != 0 && (mtu < minIPv6MTU || mtu > maxLinuxMTU) {
		return fmt.Errorf(
			"MTU must be within %d..%d or 0 (unspecified), got %d",
			minIPv6MTU,
			maxLinuxMTU,
			mtu,
		)
	}
	return nil
}

// ValidateAddresses rejects invalid prefixes and conflicting IPv6 prefix
// lengths on one link. Identical repeated addresses are permitted.
func ValidateAddresses(addresses []netip.Prefix) error {
	ipv6Prefixes := map[netip.Addr]int{}
	for idx, prefix := range addresses {
		if !prefix.IsValid() {
			return fmt.Errorf("address %d is not a valid prefix", idx)
		}
		address := prefix.Addr()
		if !address.Is6() {
			continue
		}
		if previous, exists := ipv6Prefixes[address]; exists && previous != prefix.Bits() {
			return fmt.Errorf(
				"IPv6 address %q has conflicting prefix lengths %d and %d",
				address,
				previous,
				prefix.Bits(),
			)
		}
		ipv6Prefixes[address] = prefix.Bits()
	}
	return nil
}
