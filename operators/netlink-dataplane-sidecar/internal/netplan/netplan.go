package netplan

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var managedEthernetName = regexp.MustCompile(`^kni[0-9]+$`)

const maxLinuxMTU = 1<<31 - 1

// State is the managed network state described by a netplan document.
//
// Links are ordered lexicographically by name.
type State struct {
	Links []Link
}

// Link describes a managed base Ethernet link or VLAN.
type Link struct {
	Name      string
	Parent    string
	VLANID    int
	MTU       int
	Addresses []netip.Prefix
	// AcceptRA is nil when netplan leaves the host setting unspecified.
	AcceptRA  *bool
	LinkLocal []string
}

type document struct {
	Network *network `yaml:"network"`
}

type network struct {
	Version   yaml.Node            `yaml:"version"`
	Ethernets map[string]yaml.Node `yaml:"ethernets"`
	VLANs     map[string]yaml.Node `yaml:"vlans"`
}

type linkConfig struct {
	Addresses []string  `yaml:"addresses"`
	DHCP4     bool      `yaml:"dhcp4"`
	DHCP6     bool      `yaml:"dhcp6"`
	MTU       int       `yaml:"mtu"`
	AcceptRA  *bool     `yaml:"accept-ra"`
	LinkLocal *[]string `yaml:"link-local"`
	Link      string    `yaml:"link"`
	ID        int       `yaml:"id"`
}

type vlanParent struct {
	Link string `yaml:"link"`
}

type vlanIdentity struct {
	Parent string
	ID     int
}

// Parse parses the managed state from generated netplan YAML.
func Parse(data []byte) (State, error) {
	var parsed document
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&parsed); err != nil {
		return State{}, fmt.Errorf("decode netplan YAML: %w", err)
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return State{}, errors.New("decode netplan YAML: multiple documents are not supported")
		}
		return State{}, fmt.Errorf("decode trailing netplan YAML: %w", err)
	}
	if parsed.Network == nil {
		return State{}, errors.New("network mapping is required")
	}
	if parsed.Network.Version.Kind != yaml.ScalarNode || parsed.Network.Version.Tag != "!!int" {
		return State{}, errors.New("network.version must be 2")
	}
	var version int
	if err := parsed.Network.Version.Decode(&version); err != nil || version != 2 {
		return State{}, errors.New("network.version must be 2")
	}
	if parsed.Network.Ethernets == nil {
		return State{}, errors.New("network.ethernets mapping is required")
	}
	if parsed.Network.VLANs == nil {
		return State{}, errors.New("network.vlans mapping is required")
	}

	links := make([]Link, 0, len(parsed.Network.Ethernets)+len(parsed.Network.VLANs))
	managedNames := map[string]struct{}{}
	managedParents := map[string]struct{}{}
	managedVLANs := map[vlanIdentity]string{}

	for name, node := range parsed.Network.Ethernets {
		if !managedEthernetName.MatchString(name) {
			continue
		}
		if err := validateInterfaceName(name); err != nil {
			return State{}, fmt.Errorf("ethernet %q: %w", name, err)
		}

		link, err := parseLink(name, "", 0, node)
		if err != nil {
			return State{}, err
		}
		links = append(links, link)
		managedNames[name] = struct{}{}
		managedParents[name] = struct{}{}
	}

	for name, node := range parsed.Network.VLANs {
		var parent vlanParent
		if err := node.Decode(&parent); err != nil {
			return State{}, fmt.Errorf("vlan %q: decode parent: %w", name, err)
		}
		if parent.Link == "" {
			return State{}, fmt.Errorf("vlan %q: parent link is required", name)
		}
		if _, managed := managedParents[parent.Link]; !managed {
			continue
		}
		if err := validateInterfaceName(name); err != nil {
			return State{}, fmt.Errorf("vlan %q: %w", name, err)
		}
		if _, duplicate := managedNames[name]; duplicate {
			return State{}, fmt.Errorf("duplicate managed link name %q", name)
		}

		var config linkConfig
		if err := node.Decode(&config); err != nil {
			return State{}, fmt.Errorf("vlan %q: decode configuration: %w", name, err)
		}
		if config.ID < 1 || config.ID > 4094 {
			return State{}, fmt.Errorf("vlan %q: ID %d is outside 1..4094", name, config.ID)
		}
		identity := vlanIdentity{Parent: parent.Link, ID: config.ID}
		if previous, duplicate := managedVLANs[identity]; duplicate {
			return State{}, fmt.Errorf(
				"vlan %q: parent %q ID %d is already used by %q",
				name,
				parent.Link,
				config.ID,
				previous,
			)
		}

		link, err := linkFromConfig(name, parent.Link, config.ID, config)
		if err != nil {
			return State{}, err
		}
		links = append(links, link)
		managedNames[name] = struct{}{}
		managedVLANs[identity] = name
	}

	sort.Slice(links, func(first, second int) bool {
		return links[first].Name < links[second].Name
	})
	return State{Links: links}, nil
}

// ParseFile reads and parses the managed state from a netplan YAML file.
func ParseFile(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, fmt.Errorf("read netplan file %q: %w", path, err)
	}

	state, err := Parse(data)
	if err != nil {
		return State{}, fmt.Errorf("parse netplan file %q: %w", path, err)
	}
	return state, nil
}

func parseLink(name, parent string, vlanID int, node yaml.Node) (Link, error) {
	var config linkConfig
	if err := node.Decode(&config); err != nil {
		return Link{}, fmt.Errorf("link %q: decode configuration: %w", name, err)
	}
	return linkFromConfig(name, parent, vlanID, config)
}

func linkFromConfig(name, parent string, vlanID int, config linkConfig) (Link, error) {
	if config.DHCP4 {
		return Link{}, fmt.Errorf("link %q: dhcp4 must be disabled", name)
	}
	if config.DHCP6 {
		return Link{}, fmt.Errorf("link %q: dhcp6 must be disabled", name)
	}
	if config.MTU < 0 || config.MTU > maxLinuxMTU {
		return Link{}, fmt.Errorf(
			"link %q: MTU must be within 0..%d, got %d",
			name,
			maxLinuxMTU,
			config.MTU,
		)
	}

	addresses := make([]netip.Prefix, 0, len(config.Addresses))
	for idx, address := range config.Addresses {
		prefix, err := netip.ParsePrefix(address)
		if err != nil {
			return Link{}, fmt.Errorf("link %q: address %d %q: %w", name, idx, address, err)
		}
		addresses = append(addresses, prefix)
	}

	configuredLinkLocal := []string{"ipv6"}
	if config.LinkLocal != nil {
		configuredLinkLocal = *config.LinkLocal
	}
	linkLocal := make([]string, 0, len(configuredLinkLocal))
	for idx, family := range configuredLinkLocal {
		if family != "ipv4" && family != "ipv6" {
			return Link{}, fmt.Errorf(
				"link %q: link-local value %d %q is invalid; want ipv4 or ipv6",
				name,
				idx,
				family,
			)
		}
		linkLocal = append(linkLocal, family)
	}

	var acceptRA *bool
	if config.AcceptRA != nil {
		value := *config.AcceptRA
		acceptRA = &value
	}

	return Link{
		Name:      name,
		Parent:    parent,
		VLANID:    vlanID,
		MTU:       config.MTU,
		Addresses: addresses,
		AcceptRA:  acceptRA,
		LinkLocal: linkLocal,
	}, nil
}

func validateInterfaceName(name string) error {
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
