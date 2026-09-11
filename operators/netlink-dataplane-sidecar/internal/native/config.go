package native

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config contains only the interface subset configured at sidecar startup.
type Config struct {
	Ethernets    map[string]LinkConfig `yaml:"ethernets"`
	VLANs        map[string]LinkConfig `yaml:"vlans"`
	DummyDevices map[string]LinkConfig `yaml:"dummy-devices"`
}

// LinkConfig preserves optional settings until startup normalization.
type LinkConfig struct {
	MTU       int       `yaml:"mtu"`
	Addresses []string  `yaml:"addresses"`
	LinkLocal *[]string `yaml:"link-local"`
	AcceptRA  *bool     `yaml:"accept-ra"`
	DHCP4     bool      `yaml:"dhcp4"`
	DHCP6     bool      `yaml:"dhcp6"`
	ID        *int      `yaml:"id"`
	Link      string    `yaml:"link"`
}

var decimalDigits = regexp.MustCompile(`^[0-9]+$`)

// UnmarshalYAML rejects unsupported declarations before typed decoding can
// coerce scalars, discard unknown fields or lose key presence.
func (m *Config) UnmarshalYAML(node *yaml.Node) error {
	if err := validateMapping(node); err != nil {
		return fmt.Errorf("native: %w", err)
	}
	config := Config{}
	for idx := range len(node.Content) / 2 {
		name, section := node.Content[2*idx].Value, node.Content[2*idx+1]
		var target *map[string]LinkConfig
		switch name {
		case "ethernets":
			target = &config.Ethernets
		case "vlans":
			target = &config.VLANs
		case "dummy-devices":
			target = &config.DummyDevices
		default:
			return fmt.Errorf("native: unsupported section %q", name)
		}
		if err := validateMapping(section); err != nil {
			return fmt.Errorf("native.%s: %w", name, err)
		}
		*target = map[string]LinkConfig{}
		for linkIndex := range len(section.Content) / 2 {
			linkName := section.Content[2*linkIndex].Value
			link, err := decodeLink(section.Content[2*linkIndex+1], name == "vlans")
			if err != nil {
				return fmt.Errorf("native.%s link %q: %w", name, linkName, err)
			}
			(*target)[linkName] = link
		}
	}
	*m = config
	return nil
}

func validateMapping(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" || len(node.Content)%2 != 0 {
		return errors.New("configuration mapping is required")
	}
	keys := map[string]bool{}
	for idx := range len(node.Content) / 2 {
		key := node.Content[2*idx]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return errors.New("mapping keys must be strings")
		}
		if keys[key.Value] {
			return fmt.Errorf("duplicate key %q", key.Value)
		}
		keys[key.Value] = true
	}
	return nil
}

func decodeLink(node *yaml.Node, vlan bool) (LinkConfig, error) {
	if err := validateMapping(node); err != nil {
		return LinkConfig{}, err
	}
	// Decode decimal integers independently of YAML's octal interpretation.
	normalized := *node
	normalized.Content = slices.Clone(node.Content)
	for idx := range len(node.Content) / 2 {
		key, value := node.Content[2*idx].Value, node.Content[2*idx+1]
		switch key {
		case "id", "link":
			if !vlan {
				return LinkConfig{}, fmt.Errorf("%s is only supported for VLANs", key)
			}
		case "mtu", "addresses", "link-local", "accept-ra", "dhcp4", "dhcp6":
		default:
			return LinkConfig{}, fmt.Errorf("unsupported setting %q", key)
		}
		switch key {
		case "addresses", "link-local":
			if value.Kind != yaml.SequenceNode || value.Tag != "!!seq" {
				return LinkConfig{}, fmt.Errorf("%s must be a sequence", key)
			}
			for _, item := range value.Content {
				if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
					return LinkConfig{}, fmt.Errorf("%s must contain strings", key)
				}
			}
		case "mtu", "id":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!int" || !decimalDigits.MatchString(value.Value) {
				return LinkConfig{}, fmt.Errorf("%s must be a decimal integer", key)
			}
			integer, err := strconv.ParseUint(value.Value, 10, 31)
			if err != nil {
				return LinkConfig{}, fmt.Errorf("%s: %w", key, err)
			}
			decimal := *value
			decimal.Value = strconv.FormatUint(integer, 10)
			normalized.Content[2*idx+1] = &decimal
		case "link":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return LinkConfig{}, errors.New("link must be a string")
			}
		case "accept-ra", "dhcp4", "dhcp6":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
				return LinkConfig{}, fmt.Errorf("%s must be a boolean", key)
			}
		}
	}
	var config LinkConfig
	if err := normalized.Decode(&config); err != nil {
		return LinkConfig{}, err
	}
	if err := validateLinkConfig(config, vlan); err != nil {
		return LinkConfig{}, err
	}
	return config, nil
}

func validateLinkConfig(config LinkConfig, vlan bool) error {
	if config.DHCP4 || config.DHCP6 {
		return errors.New("dhcp4 and dhcp6 must be disabled")
	}
	if vlan {
		if config.ID == nil || config.Link == "" {
			return errors.New("VLAN id and parent link are required")
		}
	} else if config.ID != nil || config.Link != "" {
		return errors.New("id and link are only supported for VLANs")
	}
	if config.LinkLocal != nil {
		families := *config.LinkLocal
		if len(families) > 1 || len(families) == 1 && families[0] != "ipv6" {
			return errors.New("link-local must be [] or [ipv6]")
		}
	}
	return nil
}
