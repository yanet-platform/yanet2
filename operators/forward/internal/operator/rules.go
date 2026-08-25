package operator

import (
	"fmt"
	"net/netip"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

// yamlVlanRange mirrors the YAML schema for a VLAN range entry.
type yamlVlanRange struct {
	From uint32 `yaml:"from"`
	To   uint32 `yaml:"to"`
}

// yamlModeKind mirrors the ModeKind enum from the Rust forward CLI.
type yamlModeKind string

const (
	modeNone yamlModeKind = "NONE"
	modeIn   yamlModeKind = "IN"
	modeOut  yamlModeKind = "OUT"
)

// legacyModeNone, legacyModeIn, and legacyModeOut are the PascalCase
// spellings of the same three modes.
//
// Rule files written before the CLI adopted the proto enum spellings use
// these forms, and those files must keep loading alongside the canonical
// uppercase ones.
const (
	legacyModeNone yamlModeKind = "None"
	legacyModeIn   yamlModeKind = "In"
	legacyModeOut  yamlModeKind = "Out"
)

// yamlForwardRule mirrors a single rule entry in the YAML forward config.
type yamlForwardRule struct {
	Target     string          `yaml:"target"`
	Mode       yamlModeKind    `yaml:"mode"`
	Counter    string          `yaml:"counter"`
	Devices    []string        `yaml:"devices"`
	VlanRanges []yamlVlanRange `yaml:"vlan_ranges"`
	Srcs       []string        `yaml:"srcs"`
	Dsts       []string        `yaml:"dsts"`
}

// yamlForwardConfig is the top-level YAML structure for a forward rules file.
type yamlForwardConfig struct {
	Rules []yamlForwardRule `yaml:"rules"`
}

// LoadForwardRules reads a YAML forward-rules file and converts it to the
// proto representation used by ForwardService.UpdateConfig.
func LoadForwardRules(path string) ([]*forwardpb.Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules file %q: %w", path, err)
	}

	var cfg yamlForwardConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse rules file %q: %w", path, err)
	}

	rules := make([]*forwardpb.Rule, 0, len(cfg.Rules))
	for idx, r := range cfg.Rules {
		rule, err := convertRule(r)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", idx, err)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// convertRule translates a single yamlForwardRule into a forwardpb.Rule.
func convertRule(r yamlForwardRule) (*forwardpb.Rule, error) {
	mode, err := convertMode(r.Mode)
	if err != nil {
		return nil, err
	}

	devices := make([]*filterpb.Device, len(r.Devices))
	for idx, d := range r.Devices {
		devices[idx] = &filterpb.Device{Name: d}
	}

	vlanRanges := make([]*filterpb.VlanRange, len(r.VlanRanges))
	for idx, vr := range r.VlanRanges {
		vlanRanges[idx] = &filterpb.VlanRange{From: vr.From, To: vr.To}
	}

	sources4, sources6, err := convertCIDRs(r.Srcs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse src: %w", err)
	}

	destinations4, destinations6, err := convertCIDRs(r.Dsts)
	if err != nil {
		return nil, fmt.Errorf("failed to parse dst: %w", err)
	}

	return &forwardpb.Rule{
		Action: &forwardpb.Action{
			Target:  r.Target,
			Mode:    mode,
			Counter: r.Counter,
		},
		Devices:       devices,
		VlanRanges:    vlanRanges,
		Sources4:      sources4,
		Sources6:      sources6,
		Destinations4: destinations4,
		Destinations6: destinations6,
	}, nil
}

// convertMode maps a yamlModeKind to the proto enum value.
func convertMode(m yamlModeKind) (forwardpb.ForwardMode, error) {
	switch m {
	case modeNone, legacyModeNone:
		return forwardpb.ForwardMode_NONE, nil
	case modeIn, legacyModeIn:
		return forwardpb.ForwardMode_IN, nil
	case modeOut, legacyModeOut:
		return forwardpb.ForwardMode_OUT, nil
	default:
		return 0, fmt.Errorf("unknown mode %q: must be NONE, IN, or OUT", m)
	}
}

// convertCIDRs parses a slice of CIDR strings into family-typed network
// messages, preserving the within-family order of the input.
//
// An IPv4-mapped IPv6 address counts as IPv6, as it did when the family
// was derived from the address byte length.
func convertCIDRs(cidrs []string) ([]*commonpb.IPv4Network, []*commonpb.IPv6Network, error) {
	var v4 []*commonpb.IPv4Network
	var v6 []*commonpb.IPv6Network
	for _, s := range cidrs {
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse CIDR %q: %w", s, err)
		}
		if prefix.Addr().Is4() {
			net, ok := xnetip.Network4FromPrefix(prefix)
			if !ok {
				return nil, nil, fmt.Errorf("invalid CIDR %q", s)
			}
			v4 = append(v4, commonpb.NewIPv4NetworkFrom4(net))
			continue
		}
		net, ok := xnetip.Network6FromPrefix(prefix)
		if !ok {
			return nil, nil, fmt.Errorf("invalid CIDR %q", s)
		}
		v6 = append(v6, commonpb.NewIPv6NetworkFrom6(net))
	}
	return v4, v6, nil
}
