package operator

import (
	"fmt"
	"net"
	"net/netip"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

// yamlVlanRange mirrors the YAML schema for a VLAN range entry.
type yamlVlanRange struct {
	From uint32 `yaml:"from"`
	To   uint32 `yaml:"to"`
}

// yamlModeKind mirrors the ModeKind enum from the Rust forward CLI.
type yamlModeKind string

const (
	modeNone yamlModeKind = "None"
	modeIn   yamlModeKind = "In"
	modeOut  yamlModeKind = "Out"
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

	srcs, err := convertCIDRs(r.Srcs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse src: %w", err)
	}

	dsts, err := convertCIDRs(r.Dsts)
	if err != nil {
		return nil, fmt.Errorf("failed to parse dst: %w", err)
	}

	return &forwardpb.Rule{
		Action: &forwardpb.Action{
			Target:  r.Target,
			Mode:    mode,
			Counter: r.Counter,
		},
		Devices:    devices,
		VlanRanges: vlanRanges,
		Srcs:       srcs,
		Dsts:       dsts,
	}, nil
}

// convertMode maps a yamlModeKind to the proto enum value.
func convertMode(m yamlModeKind) (forwardpb.ForwardMode, error) {
	switch m {
	case modeNone:
		return forwardpb.ForwardMode_NONE, nil
	case modeIn:
		return forwardpb.ForwardMode_IN, nil
	case modeOut:
		return forwardpb.ForwardMode_OUT, nil
	default:
		return 0, fmt.Errorf("unknown mode %q: must be None, In, or Out", m)
	}
}

// convertCIDRs parses a slice of CIDR strings into filterpb.IPNet values.
func convertCIDRs(cidrs []string) ([]*filterpb.IPNet, error) {
	out := make([]*filterpb.IPNet, 0, len(cidrs))
	for _, s := range cidrs {
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("failed to parse CIDR %q: %w", s, err)
		}
		addr := prefix.Masked().Addr().AsSlice()
		mask := net.CIDRMask(prefix.Bits(), len(addr)*8)
		out = append(out, &filterpb.IPNet{
			Addr: addr,
			Mask: mask,
		})
	}
	return out, nil
}
