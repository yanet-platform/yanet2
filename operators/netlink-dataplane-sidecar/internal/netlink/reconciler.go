package netlink

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"

	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Backend permits interface restoration and narrowly scoped IPv6LL cleanup.
type Backend interface {
	LinkList() ([]vnetlink.Link, error)
	LinkByName(string) (vnetlink.Link, error)
	LinkAdd(vnetlink.Link) error
	LinkSetMTU(vnetlink.Link, int) error
	LinkSetUp(vnetlink.Link) error
	AddrList(vnetlink.Link, int) ([]vnetlink.Addr, error)
	AddrReplace(vnetlink.Link, *vnetlink.Addr) error
	AddrDel(vnetlink.Link, *vnetlink.Addr) error
}

// Sysctl writes a per-interface setting after validating its open descriptor.
type Sysctl interface {
	SetIPv6(context.Context, string, string, string, func() error) error
}

// Reconciler restores desired values without retaining historical ownership.
type Reconciler struct {
	backend   Backend
	sysctl    Sysctl
	applySlot chan struct{}
}

// NewReconciler serializes restoration through the supplied kernel handle.
func NewReconciler(backend Backend, sysctl Sysctl) *Reconciler {
	return &Reconciler{backend: backend, sysctl: sysctl, applySlot: make(chan struct{}, 1)}
}

// Apply preserves successful partial setup for the next idempotent pass.
func (m *Reconciler) Apply(ctx context.Context, state netplan.State) error {
	if m.backend == nil || m.sysctl == nil || m.applySlot == nil {
		return errors.New("apply network state: uninitialized reconciler")
	}
	select {
	case m.applySlot <- struct{}{}:
		defer func() { <-m.applySlot }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := state.Validate(); err != nil {
		return err
	}
	links, err := m.backend.LinkList()
	if err != nil {
		return fmt.Errorf("list links: %w", err)
	}
	existing := map[string]vnetlink.Link{}
	for _, link := range links {
		identity, err := IdentifyLink(link)
		if err != nil {
			return err
		}
		if _, duplicate := existing[identity.Name]; duplicate {
			return fmt.Errorf("duplicate link %q in dump", identity.Name)
		}
		existing[identity.Name] = link
	}
	identities := map[string]LinkIdentity{}
	for _, wanted := range state.Links {
		link := existing[wanted.Name]
		if link == nil {
			if wanted.Kind == netplan.LinkKindKNI || wanted.Kind == netplan.LinkKindLoopback {
				return fmt.Errorf("kernel link %q is not available yet", wanted.Name)
			}
			continue
		}
		if err := ValidateLink(wanted, link, existing[wanted.Parent]); err != nil {
			return err
		}
		identities[wanted.Name], _ = IdentifyLink(link)
	}
	if err := validateEffectiveMTUs(state, existing); err != nil {
		return err
	}

	// Parent increases precede VLAN creation and child MTU changes.
	for _, wanted := range state.Links {
		if wanted.Kind != netplan.LinkKindKNI || wanted.MTU == 0 {
			continue
		}
		if existing[wanted.Name].Attrs().MTU < wanted.MTU {
			if err := m.ensureMTU(ctx, wanted, identities); err != nil {
				return err
			}
		}
	}
	for _, wanted := range state.Links {
		if _, present := identities[wanted.Name]; present {
			continue
		}
		attributes := vnetlink.LinkAttrs{Name: wanted.Name, MTU: wanted.MTU}
		var created vnetlink.Link = &vnetlink.Dummy{LinkAttrs: attributes}
		var parent vnetlink.Link
		if wanted.Kind == netplan.LinkKindVLAN {
			parent, err = m.resolve(ctx, identities[wanted.Parent])
			if err != nil {
				return err
			}
			attributes.ParentIndex = parent.Attrs().Index
			if attributes.MTU == 0 {
				attributes.MTU = parent.Attrs().MTU
				for _, desiredParent := range state.Links {
					if desiredParent.Name == wanted.Parent && desiredParent.MTU != 0 {
						attributes.MTU = desiredParent.MTU
						break
					}
				}
			}
			created = &vnetlink.Vlan{
				LinkAttrs: attributes, VlanId: wanted.VLANID,
				VlanProtocol: vnetlink.VLAN_PROTOCOL_8021Q,
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.backend.LinkAdd(created); err != nil {
			return fmt.Errorf("create link %q: %w", wanted.Name, err)
		}
		link, err := m.backend.LinkByName(wanted.Name)
		if err != nil {
			return fmt.Errorf("find created link %q: %w", wanted.Name, err)
		}
		if err := ValidateLink(wanted, link, parent); err != nil {
			return err
		}
		identities[wanted.Name], err = IdentifyLink(link)
		if err != nil {
			return err
		}
	}
	configurationOrder := slices.Clone(state.Links)
	slices.SortStableFunc(configurationOrder, func(left, right netplan.Link) int {
		leftVLAN, rightVLAN := left.Kind == netplan.LinkKindVLAN, right.Kind == netplan.LinkKindVLAN
		if leftVLAN == rightVLAN {
			return 0
		}
		if leftVLAN {
			return 1
		}
		return -1
	})
	for _, wanted := range configurationOrder {
		if wanted.Kind != netplan.LinkKindKNI {
			if err := m.ensureMTU(ctx, wanted, identities); err != nil {
				return err
			}
		}
		if err := m.configureLink(ctx, wanted, identities); err != nil {
			return fmt.Errorf("configure link %q: %w", wanted.Name, err)
		}
	}
	// Parent decreases follow all child restoration.
	for _, wanted := range state.Links {
		if wanted.Kind == netplan.LinkKindKNI {
			if err := m.ensureMTU(ctx, wanted, identities); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateEffectiveMTUs(state netplan.State, existing map[string]vnetlink.Link) error {
	desired := map[string]netplan.Link{}
	for _, link := range state.Links {
		desired[link.Name] = link
		if link.MTU == 0 && existing[link.Name] != nil {
			if err := netplan.ValidateMTU(existing[link.Name].Attrs().MTU); err != nil {
				return fmt.Errorf("link %q: %w", link.Name, err)
			}
		}
	}
	for _, child := range state.Links {
		if child.Kind != netplan.LinkKindVLAN {
			continue
		}
		parentMTU := desired[child.Parent].MTU
		if parentMTU == 0 {
			parentMTU = existing[child.Parent].Attrs().MTU
		}
		childMTU := child.MTU
		if childMTU == 0 && existing[child.Name] != nil {
			childMTU = existing[child.Name].Attrs().MTU
		}
		if childMTU > parentMTU {
			return fmt.Errorf("VLAN %q MTU %d exceeds parent MTU %d", child.Name, childMTU, parentMTU)
		}
	}
	return nil
}

func (m *Reconciler) resolve(ctx context.Context, expected LinkIdentity) (vnetlink.Link, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	link, err := m.backend.LinkByName(expected.Name)
	if err != nil {
		return nil, fmt.Errorf("revalidate link %q: %w", expected.Name, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := IdentifyLink(link)
	if err != nil {
		return nil, err
	}
	if current != expected {
		return nil, fmt.Errorf("link %q changed identity during restoration", expected.Name)
	}
	return link, nil
}

func (m *Reconciler) resolveConfigured(
	ctx context.Context, wanted netplan.Link, identities map[string]LinkIdentity,
) (vnetlink.Link, error) {
	if wanted.Parent != "" {
		if _, err := m.resolve(ctx, identities[wanted.Parent]); err != nil {
			return nil, err
		}
	}
	return m.resolve(ctx, identities[wanted.Name])
}

func (m *Reconciler) ensureMTU(ctx context.Context, wanted netplan.Link, identities map[string]LinkIdentity) error {
	if wanted.MTU == 0 {
		return nil
	}
	link, err := m.resolveConfigured(ctx, wanted, identities)
	if err != nil {
		return err
	}
	if link.Attrs().MTU == wanted.MTU {
		return nil
	}
	if wanted.Kind == netplan.LinkKindKNI && link.Attrs().MTU > wanted.MTU {
		links, err := m.backend.LinkList()
		if err != nil {
			return err
		}
		for _, child := range links {
			if child == nil || child.Attrs() == nil {
				return errors.New("incomplete child link dump")
			}
			// A veth link index names its peer, not an MTU-dependent child.
			if child.Type() != "veth" && child.Attrs().ParentIndex == link.Attrs().Index && child.Attrs().MTU > wanted.MTU {
				return fmt.Errorf("child %q exceeds desired parent MTU", child.Attrs().Name)
			}
		}
		link, err = m.resolveConfigured(ctx, wanted, identities)
		if err != nil {
			return err
		}
	}
	if err := m.backend.LinkSetMTU(link, wanted.MTU); err != nil {
		return fmt.Errorf("set MTU on %q: %w", wanted.Name, err)
	}
	return nil
}

func (m *Reconciler) configureLink(ctx context.Context, wanted netplan.Link, identities map[string]LinkIdentity) error {
	validate := func() error {
		_, err := m.resolveConfigured(ctx, wanted, identities)
		return err
	}
	settings := []struct{ Name, Value string }{{"addr_gen_mode", "1"}}
	if wanted.IPv6LinkLocal {
		settings[0].Value = "0"
	}
	if wanted.AcceptRA != nil {
		value := "0"
		if *wanted.AcceptRA {
			// Router advertisements remain usable while forwarding is enabled.
			value = "2"
		}
		settings = append(settings, struct{ Name, Value string }{"accept_ra", value})
	}
	settings = append(settings, struct{ Name, Value string }{"disable_ipv6", "0"})
	for _, setting := range settings {
		if err := m.sysctl.SetIPv6(ctx, wanted.Name, setting.Name, setting.Value, validate); err != nil {
			return err
		}
	}
	link, err := m.resolveConfigured(ctx, wanted, identities)
	if err != nil {
		return err
	}
	if link.Attrs().Flags&net.FlagUp == 0 {
		if err := m.backend.LinkSetUp(link); err != nil {
			return err
		}
	}
	addresses, err := m.backend.AddrList(link, vnetlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("list addresses: %w", err)
	}
	for _, desired := range wanted.Addresses {
		present := false
		for _, address := range addresses {
			if address.IPNet == nil || !address.IP.Equal(desired.Addr().AsSlice()) {
				continue
			}
			bits, _ := address.Mask.Size()
			if bits != desired.Bits() && desired.Addr().Is6() {
				return fmt.Errorf("IPv6 address %s has incompatible prefix length %d", desired.Addr(), bits)
			}
			if desired.Addr().Is6() && address.Flags&unix.IFA_F_DADFAILED != 0 {
				link, err = m.resolveConfigured(ctx, wanted, identities)
				if err != nil {
					return err
				}
				if err := m.backend.AddrDel(link, &address); err != nil {
					return fmt.Errorf("remove failed-DAD address %s: %w", desired, err)
				}
				continue
			}
			present = present || bits == desired.Bits()
		}
		if present {
			continue
		}
		link, err = m.resolveConfigured(ctx, wanted, identities)
		if err != nil {
			return err
		}
		address := vnetlink.Addr{IPNet: &net.IPNet{
			IP: desired.Addr().AsSlice(), Mask: net.CIDRMask(desired.Bits(), desired.Addr().BitLen()),
		}}
		if err := m.backend.AddrReplace(link, &address); err != nil {
			return fmt.Errorf("ensure address %s: %w", desired, err)
		}
	}
	link, err = m.resolveConfigured(ctx, wanted, identities)
	if err != nil {
		return err
	}
	addresses, err = m.backend.AddrList(link, vnetlink.FAMILY_V6)
	if err != nil {
		return fmt.Errorf("check IPv6 address readiness: %w", err)
	}
	for _, address := range addresses {
		if address.IPNet == nil || address.Flags&(unix.IFA_F_DADFAILED|unix.IFA_F_TENTATIVE) == 0 {
			continue
		}
		if slices.ContainsFunc(wanted.Addresses, func(prefix netip.Prefix) bool {
			return prefix.Addr().Is6() && address.IP.Equal(prefix.Addr().AsSlice())
		}) {
			return fmt.Errorf("desired IPv6 address %s has not completed duplicate address detection", address.IP)
		}
	}
	if wanted.Kind != netplan.LinkKindLoopback && !wanted.IPv6LinkLocal {
		return m.removeUnlistedIPv6LL(ctx, wanted, identities)
	}
	return validate()
}

func (m *Reconciler) removeUnlistedIPv6LL(ctx context.Context, wanted netplan.Link, identities map[string]LinkIdentity) error {
	link, err := m.resolveConfigured(ctx, wanted, identities)
	if err != nil {
		return err
	}
	addresses, err := m.backend.AddrList(link, vnetlink.FAMILY_V6)
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if address.IPNet == nil || address.IP.To4() != nil || !address.IP.IsLinkLocalUnicast() {
			continue
		}
		if slices.ContainsFunc(wanted.Addresses, func(prefix netip.Prefix) bool {
			return address.IP.Equal(prefix.Addr().AsSlice())
		}) {
			continue
		}
		link, err = m.resolveConfigured(ctx, wanted, identities)
		if err != nil {
			return err
		}
		if err := m.backend.AddrDel(link, &address); err != nil {
			return fmt.Errorf("remove unlisted IPv6 link-local address: %w", err)
		}
	}
	_, err = m.resolveConfigured(ctx, wanted, identities)
	return err
}

var _ Backend = (*vnetlink.Handle)(nil)
