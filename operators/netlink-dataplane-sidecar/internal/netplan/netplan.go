package netplan

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
)

var decimalDigits = regexp.MustCompile(`^[0-9]+$`)

// Parse reads the managed interface subset of a single Netplan document.
func Parse(data []byte) (desired.State, error) {
	var root map[string]yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&root); err != nil {
		return desired.State{}, fmt.Errorf("decode netplan YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return desired.State{}, errors.New("decode netplan YAML: multiple documents are not supported")
		}
		return desired.State{}, fmt.Errorf("decode trailing netplan YAML: %w", err)
	}
	network, err := mapping(root["network"])
	if err != nil {
		return desired.State{}, fmt.Errorf("network: %w", err)
	}
	version := scalar(network["version"])
	if version.Kind != yaml.ScalarNode || version.Tag != "!!int" || version.Value != "2" {
		return desired.State{}, errors.New("network.version must be 2")
	}
	for _, name := range slices.Sorted(maps.Keys(network)) {
		switch name {
		case "version", "renderer", "ethernets", "vlans", "dummy-devices",
			"wifis", "modems", "bridges", "bonds", "tunnels", "vrfs",
			"nm-devices", "virtual-ethernets", "openvswitch":
		default:
			return desired.State{}, fmt.Errorf("network: unsupported setting %q", name)
		}
	}
	sections := map[string]map[string]yaml.Node{}
	for _, name := range []string{"ethernets", "vlans", "dummy-devices"} {
		if node, present := network[name]; present {
			section, err := mapping(node)
			if err != nil {
				return desired.State{}, fmt.Errorf("network.%s: %w", name, err)
			}
			sections[name] = section
		}
	}

	state := desired.State{}
	for _, name := range slices.Sorted(maps.Keys(sections["ethernets"])) {
		node := sections["ethernets"][name]
		kind := desired.LinkKindKNI
		if name == "lo" {
			kind = desired.LinkKindLoopback
		} else if !desired.IsKNIName(name) {
			continue
		}
		link, err := parseLink(name, kind, node)
		if err != nil {
			return desired.State{}, err
		}
		state.Links = append(state.Links, link)
	}
	for _, name := range slices.Sorted(maps.Keys(sections["dummy-devices"])) {
		node := sections["dummy-devices"][name]
		link, err := parseLink(name, desired.LinkKindDummy, node)
		if err != nil {
			return desired.State{}, err
		}
		state.Links = append(state.Links, link)
	}
	for _, name := range slices.Sorted(maps.Keys(sections["vlans"])) {
		node := sections["vlans"][name]
		fields, err := mapping(node)
		if err != nil {
			return desired.State{}, fmt.Errorf("vlan %q: %w", name, err)
		}
		parentNode := scalar(fields["link"])
		if parentNode.Kind != yaml.ScalarNode || parentNode.Tag != "!!str" || parentNode.Value == "" {
			return desired.State{}, fmt.Errorf("vlan %q: parent link is required", name)
		}
		parent := parentNode.Value
		if _, declared := sections["ethernets"][parent]; !declared || parent == "lo" {
			return desired.State{}, fmt.Errorf("vlan %q: parent %q is not a declared Ethernet", name, parent)
		}
		if !desired.IsKNIName(parent) {
			continue
		}
		link, err := parseLink(name, desired.LinkKindVLAN, node)
		if err != nil {
			return desired.State{}, err
		}
		link.Parent = parent
		state.Links = append(state.Links, link)
	}
	sort.Slice(state.Links, func(first, second int) bool {
		return state.Links[first].Name < state.Links[second].Name
	})
	if err := state.Validate(); err != nil {
		return desired.State{}, err
	}
	return state, nil
}

// ParseFile reads and validates the startup Netplan file.
func ParseFile(path string) (desired.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return desired.State{}, fmt.Errorf("read netplan file %q: %w", path, err)
	}
	state, err := Parse(data)
	if err != nil {
		return desired.State{}, fmt.Errorf("parse netplan file %q: %w", path, err)
	}
	return state, nil
}

func mapping(node yaml.Node) (map[string]yaml.Node, error) {
	node = scalar(node)
	if node.Kind != yaml.MappingNode {
		return nil, errors.New("configuration mapping is required")
	}
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func scalar(node yaml.Node) yaml.Node {
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = *node.Alias
	}
	return node
}

func parseLink(name string, kind desired.LinkKind, node yaml.Node) (desired.Link, error) {
	fields, err := mapping(node)
	if err != nil {
		return desired.Link{}, fmt.Errorf("link %q: %w", name, err)
	}
	link := desired.Link{Name: name, Kind: kind, IPv6LinkLocal: true}
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		value := scalar(fields[key])
		switch key {
		case "routes", "routing-policy":
			continue
		case "addresses", "link-local":
			if value.Kind != yaml.SequenceNode {
				return desired.Link{}, fmt.Errorf("link %q: %s must be a sequence", name, key)
			}
			values := []string{}
			for _, item := range value.Content {
				itemValue := scalar(*item)
				if itemValue.Kind != yaml.ScalarNode || itemValue.Tag != "!!str" {
					return desired.Link{}, fmt.Errorf("link %q: %s must contain strings", name, key)
				}
				values = append(values, itemValue.Value)
			}
			if key == "link-local" {
				for _, family := range values {
					if family != "ipv6" {
						return desired.Link{}, fmt.Errorf("link %q: unsupported link-local family %q", name, family)
					}
				}
				link.IPv6LinkLocal = len(values) != 0
				continue
			}
			for _, address := range values {
				prefix, err := netip.ParsePrefix(address)
				if err != nil {
					return desired.Link{}, fmt.Errorf("link %q: address %q: %w", name, address, err)
				}
				link.Addresses = append(link.Addresses, prefix)
			}
		case "dhcp4", "dhcp6", "accept-ra":
			var enabled bool
			if value.Kind != yaml.ScalarNode || value.Tag == "!!null" || key != "accept-ra" && value.Tag != "!!bool" {
				return desired.Link{}, fmt.Errorf("link %q: %s must be a boolean", name, key)
			}
			if err := value.Decode(&enabled); err != nil {
				return desired.Link{}, fmt.Errorf("link %q: %s: %w", name, key, err)
			}
			if key == "accept-ra" {
				link.AcceptRA = &enabled
			} else if enabled {
				return desired.Link{}, fmt.Errorf("link %q: %s must be disabled", name, key)
			}
		case "mtu":
			if value.Kind != yaml.ScalarNode || !decimalDigits.MatchString(value.Value) {
				return desired.Link{}, fmt.Errorf("link %q: mtu must be a decimal integer", name)
			}
			mtu, err := strconv.ParseUint(value.Value, 10, 31)
			if err != nil {
				return desired.Link{}, fmt.Errorf("link %q: mtu: %w", name, err)
			}
			link.MTU = int(mtu)
		case "id", "link":
			if kind != desired.LinkKindVLAN {
				return desired.Link{}, fmt.Errorf("link %q: %s is only supported for VLANs", name, key)
			}
		default:
			return desired.Link{}, fmt.Errorf("link %q: unsupported setting %q", name, key)
		}
	}
	if kind == desired.LinkKindVLAN {
		value := scalar(fields["id"])
		if value.Kind != yaml.ScalarNode || value.Tag == "!!null" || !decimalDigits.MatchString(value.Value) {
			return desired.Link{}, fmt.Errorf("vlan %q: id must be decimal digits in 0..4094", name)
		}
		identifier, err := strconv.ParseUint(value.Value, 10, 16)
		if err != nil || identifier > 4094 {
			return desired.Link{}, fmt.Errorf("vlan %q: id must be in 0..4094", name)
		}
		link.VLANID = int(identifier)
	}
	return link, nil
}
