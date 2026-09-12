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

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
)

// Backend permits interface setup and narrowly scoped IPv6LL cleanup.
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

// Reconciler separates interface creation from configuration during bootstrap.
//
// One worker retries these operations until success, then stops calling them.
type Reconciler struct {
	backend Backend
	sysctl  Sysctl
}

// NewReconciler uses the supplied kernel handle for startup mutations.
func NewReconciler(backend Backend, sysctl Sysctl) *Reconciler {
	return &Reconciler{backend: backend, sysctl: sysctl}
}

func (m *Reconciler) readLinks(ctx context.Context, state desired.State) (map[string]vnetlink.Link, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.backend == nil {
		return nil, errors.New("interface setup: netlink backend is nil")
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}
	links, err := m.backend.LinkList()
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	existing := map[string]vnetlink.Link{}
	for _, link := range links {
		identity, err := IdentifyLink(link)
		if err != nil {
			return nil, err
		}
		if _, duplicate := existing[identity.Name]; duplicate {
			return nil, fmt.Errorf("duplicate link %q in dump", identity.Name)
		}
		existing[identity.Name] = link
	}
	return existing, nil
}

// Create ensures dummy and VLAN existence without configuring existing links.
//
// KNI and loopback must be supplied by the kernel/dataplane. A VLAN whose MTU
// exceeds its observed parent waits for parent configuration on a later retry.
func (m *Reconciler) Create(ctx context.Context, state desired.State) error {
	existing, err := m.readLinks(ctx, state)
	if err != nil {
		return err
	}
	var failures error
	for _, wanted := range state.Links {
		failures = errors.Join(failures, m.createLink(ctx, wanted, state, existing))
	}
	return failures
}

func (m *Reconciler) createLink(ctx context.Context, wanted desired.Link, state desired.State, existing map[string]vnetlink.Link) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent := existing[wanted.Parent]
	if link := existing[wanted.Name]; link != nil {
		return ValidateLink(wanted, link, parent)
	}
	if wanted.Kind == desired.LinkKindKNI || wanted.Kind == desired.LinkKindLoopback {
		return fmt.Errorf("kernel link %q is not available yet", wanted.Name)
	}
	attributes := vnetlink.LinkAttrs{Name: wanted.Name, MTU: wanted.MTU}
	var created vnetlink.Link = &vnetlink.Dummy{LinkAttrs: attributes}
	if wanted.Kind == desired.LinkKindVLAN {
		if err := ValidateLink(desired.Link{Name: wanted.Parent}, parent, nil); err != nil {
			return fmt.Errorf("create VLAN %q: parent %q: %w", wanted.Name, wanted.Parent, err)
		}
		identity, _ := IdentifyLink(parent)
		var err error
		parent, err = m.resolve(ctx, identity)
		if err != nil {
			return err
		}
		attributes.ParentIndex = parent.Attrs().Index
		if attributes.MTU == 0 {
			attributes.MTU = parent.Attrs().MTU
			for _, desiredParent := range state.Links {
				if desiredParent.Name == wanted.Parent && desiredParent.MTU != 0 {
					attributes.MTU = desiredParent.MTU
				}
			}
		}
		created = &vnetlink.Vlan{
			LinkAttrs: attributes, VlanId: wanted.VLANID,
			VlanProtocol: vnetlink.VLAN_PROTOCOL_8021Q,
		}
	}
	if err := m.backend.LinkAdd(created); err != nil {
		return fmt.Errorf("create link %q: %w", wanted.Name, err)
	}
	return nil
}

// Configure attempts every available link without waiting for missing ones.
//
// MTU increases precede child changes; parent decreases follow them and refuse
// to clamp any remaining oversized child. Partial setup is safe to retry.
func (m *Reconciler) Configure(ctx context.Context, state desired.State) error {
	existing, err := m.readLinks(ctx, state)
	if err != nil {
		return err
	}
	if m.sysctl == nil {
		return errors.New("configure interfaces: sysctl backend is nil")
	}
	identities := map[string]LinkIdentity{}
	var failures error
	for _, wanted := range state.Links {
		link := existing[wanted.Name]
		if err := ValidateLink(wanted, link, existing[wanted.Parent]); err != nil {
			failures = errors.Join(failures, fmt.Errorf("configure link %q: %w", wanted.Name, err))
			continue
		}
		if err := validateEffectiveMTU(wanted, state, existing); err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		identities[wanted.Name], _ = IdentifyLink(link)
	}
	configurationOrder := slices.Clone(state.Links)
	slices.SortStableFunc(configurationOrder, func(left, right desired.Link) int {
		leftVLAN, rightVLAN := left.Kind == desired.LinkKindVLAN, right.Kind == desired.LinkKindVLAN
		if leftVLAN == rightVLAN {
			return 0
		}
		if leftVLAN {
			return 1
		}
		return -1
	})
	for _, wanted := range configurationOrder {
		if _, present := identities[wanted.Name]; !present {
			continue
		}
		if wanted.Kind != desired.LinkKindKNI || existing[wanted.Name].Attrs().MTU < wanted.MTU {
			if err := m.ensureMTU(ctx, wanted, identities); err != nil {
				failures = errors.Join(failures, err)
				continue
			}
		}
		if err := m.configureLink(ctx, wanted, identities); err != nil {
			failures = errors.Join(failures, fmt.Errorf("configure link %q: %w", wanted.Name, err))
		}
	}
	for _, wanted := range state.Links {
		if _, present := identities[wanted.Name]; present && wanted.Kind == desired.LinkKindKNI {
			failures = errors.Join(failures, m.ensureMTU(ctx, wanted, identities))
		}
	}
	return failures
}

func validateEffectiveMTU(wanted desired.Link, state desired.State, existing map[string]vnetlink.Link) error {
	mtu := wanted.MTU
	if mtu == 0 {
		mtu = existing[wanted.Name].Attrs().MTU
	}
	if err := desired.ValidateMTU(mtu); err != nil {
		return fmt.Errorf("link %q: %w", wanted.Name, err)
	}
	if wanted.Kind == desired.LinkKindVLAN {
		parentMTU := existing[wanted.Parent].Attrs().MTU
		for _, parent := range state.Links {
			if parent.Name == wanted.Parent && parent.MTU != 0 {
				parentMTU = parent.MTU
			}
		}
		if mtu > parentMTU {
			return fmt.Errorf("VLAN %q MTU %d exceeds parent MTU %d", wanted.Name, mtu, parentMTU)
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
		return nil, fmt.Errorf("link %q changed identity during setup", expected.Name)
	}
	return link, nil
}

func (m *Reconciler) resolveConfigured(
	ctx context.Context, wanted desired.Link, identities map[string]LinkIdentity,
) (vnetlink.Link, error) {
	if wanted.Parent != "" {
		if _, err := m.resolve(ctx, identities[wanted.Parent]); err != nil {
			return nil, err
		}
	}
	return m.resolve(ctx, identities[wanted.Name])
}

func (m *Reconciler) ensureMTU(ctx context.Context, wanted desired.Link, identities map[string]LinkIdentity) error {
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
	if wanted.Kind == desired.LinkKindKNI && link.Attrs().MTU > wanted.MTU {
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

func (m *Reconciler) configureLink(ctx context.Context, wanted desired.Link, identities map[string]LinkIdentity) error {
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
	if wanted.Kind != desired.LinkKindLoopback && !wanted.IPv6LinkLocal {
		return m.removeUnlistedIPv6LL(ctx, wanted, identities)
	}
	return validate()
}

func (m *Reconciler) removeUnlistedIPv6LL(ctx context.Context, wanted desired.Link, identities map[string]LinkIdentity) error {
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
