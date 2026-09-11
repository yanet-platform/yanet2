package native

import (
	"cmp"
	"fmt"
	"maps"
	"net/netip"
	"slices"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
)

// Source normalizes the embedded startup configuration without external files.
type Source struct {
	Config Config
}

// Load returns a validated state that owns all of its mutable configuration.
func (m *Source) Load() (desired.State, error) {
	state := desired.State{}
	for _, section := range []struct {
		Links map[string]LinkConfig
		Kind  desired.LinkKind
	}{
		{m.Config.Ethernets, desired.LinkKindKNI},
		{m.Config.VLANs, desired.LinkKindVLAN},
		{m.Config.DummyDevices, desired.LinkKindDummy},
	} {
		for _, name := range slices.Sorted(maps.Keys(section.Links)) {
			config := section.Links[name]
			if err := validateLinkConfig(config, section.Kind == desired.LinkKindVLAN); err != nil {
				return desired.State{}, fmt.Errorf("native link %q: %w", name, err)
			}
			link := desired.Link{
				Name: name, Kind: section.Kind, Parent: config.Link,
				MTU: config.MTU, IPv6LinkLocal: true,
			}
			if section.Kind == desired.LinkKindKNI && name == "lo" {
				link.Kind = desired.LinkKindLoopback
			}
			if config.ID != nil {
				link.VLANID = *config.ID
			}
			if config.LinkLocal != nil {
				link.IPv6LinkLocal = len(*config.LinkLocal) != 0
			}
			if config.AcceptRA != nil {
				value := *config.AcceptRA
				link.AcceptRA = &value
			}
			for _, address := range config.Addresses {
				prefix, err := netip.ParsePrefix(address)
				if err != nil {
					return desired.State{}, fmt.Errorf("native link %q: address %q: %w", name, address, err)
				}
				link.Addresses = append(link.Addresses, prefix)
			}
			state.Links = append(state.Links, link)
		}
	}
	slices.SortFunc(state.Links, func(left, right desired.Link) int {
		return cmp.Compare(left.Name, right.Name)
	})
	if err := state.Validate(); err != nil {
		return desired.State{}, fmt.Errorf("native: %w", err)
	}
	return state, nil
}

var _ desired.Source = (*Source)(nil)
