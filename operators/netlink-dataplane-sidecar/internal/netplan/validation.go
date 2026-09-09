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
// Smaller MTUs disable IPv6 and discard configured addresses.
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

// Validate rejects an inconsistent managed topology before kernel operations.
func (m State) Validate() error {
	links := map[string]Link{}
	vlans := map[struct {
		Parent string
		ID     int
	}]string{}
	for _, link := range m.Links {
		if err := ValidateInterfaceName(link.Name); err != nil {
			return fmt.Errorf("link %q: %w", link.Name, err)
		}
		if err := ValidateMTU(link.MTU); err != nil {
			return fmt.Errorf("link %q: %w", link.Name, err)
		}
		if err := ValidateAddresses(link.Addresses); err != nil {
			return fmt.Errorf("link %q: %w", link.Name, err)
		}
		for _, family := range link.LinkLocal {
			if family != "ipv6" {
				return fmt.Errorf("link %q: unsupported link-local family %q", link.Name, family)
			}
		}
		if _, duplicate := links[link.Name]; duplicate {
			return fmt.Errorf("duplicate managed link name %q", link.Name)
		}
		links[link.Name] = link
		switch link.Kind {
		case LinkKindKNI:
			if !managedEthernetName.MatchString(link.Name) || link.Parent != "" {
				return fmt.Errorf("link %q: expected a base KNI", link.Name)
			}
		case LinkKindLoopback:
			if link.Name != "lo" || link.Parent != "" {
				return fmt.Errorf("link %q: expected kernel loopback lo", link.Name)
			}
		case LinkKindDummy, LinkKindVLAN:
			if link.Name == "lo" || link.Name == "eth0" || link.Name == "eth1" || managedEthernetName.MatchString(link.Name) {
				return fmt.Errorf("link %q: name is reserved for an existing interface", link.Name)
			}
			if link.Kind == LinkKindDummy && link.Parent != "" {
				return fmt.Errorf("dummy %q: parent is not supported", link.Name)
			}
		default:
			return fmt.Errorf("link %q: unsupported kind %d", link.Name, link.Kind)
		}
	}
	for _, link := range m.Links {
		if link.Kind != LinkKindVLAN {
			continue
		}
		parent, found := links[link.Parent]
		if !found || parent.Kind != LinkKindKNI {
			return fmt.Errorf("vlan %q: parent %q is not a managed KNI", link.Name, link.Parent)
		}
		if link.VLANID < 0 || link.VLANID > 4094 {
			return fmt.Errorf("vlan %q: id must be in 0..4094", link.Name)
		}
		identity := struct {
			Parent string
			ID     int
		}{link.Parent, link.VLANID}
		if previous, duplicate := vlans[identity]; duplicate {
			return fmt.Errorf("vlan %q: parent/ID is already used by %q", link.Name, previous)
		}
		vlans[identity] = link.Name
		if link.MTU != 0 && parent.MTU != 0 && link.MTU > parent.MTU {
			return fmt.Errorf("vlan %q: MTU exceeds parent %q", link.Name, link.Parent)
		}
	}
	return nil
}
