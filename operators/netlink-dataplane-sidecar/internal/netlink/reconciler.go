package netlink

import (
	"bytes"
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

// ManagedAlias records an explicit, exclusive lifecycle handoff to this
// sidecar. Other managers must not mutate a marked link or attach addresses to
// it while the sidecar is running.
const ManagedAlias = "yanet-netlink-dataplane-sidecar"

const (
	managedAlias        = ManagedAlias
	managedVLANProtocol = vnetlink.VLAN_PROTOCOL_8021Q
)

// Backend is the subset of a netlink handle used by Reconciler.
type Backend interface {
	LinkList() ([]vnetlink.Link, error)
	LinkByName(string) (vnetlink.Link, error)
	LinkAdd(vnetlink.Link) error
	LinkDel(vnetlink.Link) error
	LinkSetMTU(vnetlink.Link, int) error
	LinkSetUp(vnetlink.Link) error
	AddrList(vnetlink.Link, int) ([]vnetlink.Addr, error)
	AddrReplace(vnetlink.Link, *vnetlink.Addr) error
	AddrDel(vnetlink.Link, *vnetlink.Addr) error
}

// Sysctl applies an IPv6 sysctl to one network interface.
type Sysctl interface {
	SetIPv6(context.Context, string, string, string, func() error) error
}

// Reconciler applies netplan state through a netlink handle and sysctl writer.
type Reconciler struct {
	backend        Backend
	sysctl         Sysctl
	applySlot      chan struct{}
	ownedAddresses map[string]ownedLinkAddresses
}

type ownedLinkAddresses struct {
	Identity  linkIdentity
	Addresses map[string]struct{}
}

type linkIdentity struct {
	Name            string
	Index           int
	Type            string
	Alias           string
	ParentIndex     int
	HardwareAddress string
	IsVLAN          bool
	VLANID          int
	VLANProtocol    vnetlink.VlanProtocol
}

type vlanIdentity struct {
	Parent string
	ID     int
}

// NewReconciler creates a reconciler. A *netlink.Handle can be passed as backend.
func NewReconciler(backend Backend, sysctl Sysctl) *Reconciler {
	return &Reconciler{
		backend:        backend,
		sysctl:         sysctl,
		applySlot:      make(chan struct{}, 1),
		ownedAddresses: map[string]ownedLinkAddresses{},
	}
}

// Apply reconciles managed links, their addresses, and IPv6 link settings.
func (m *Reconciler) Apply(ctx context.Context, state netplan.State) error {
	if m.applySlot == nil {
		return applyError("initialize reconciler", errors.New("nil apply slot"))
	}
	select {
	case m.applySlot <- struct{}{}:
		defer func() { <-m.applySlot }()
	case <-ctx.Done():
		return applyError("wait for previous reconciliation", ctx.Err())
	}

	if m.backend == nil {
		return applyError("initialize backend", errors.New("nil netlink backend"))
	}
	if m.sysctl == nil {
		return applyError("initialize sysctl", errors.New("nil sysctl writer"))
	}
	if m.ownedAddresses == nil {
		m.ownedAddresses = map[string]ownedLinkAddresses{}
	}
	if err := ctx.Err(); err != nil {
		return applyError("check context", err)
	}

	desired := make(map[string]netplan.Link, len(state.Links))
	desiredVLANs := make(map[vlanIdentity]string, len(state.Links))
	for _, link := range state.Links {
		if err := netplan.ValidateInterfaceName(link.Name); err != nil {
			return applyError(fmt.Sprintf("validate link %q", link.Name), err)
		}
		if err := netplan.ValidateMTU(link.MTU); err != nil {
			return applyError(fmt.Sprintf("validate link %q", link.Name), err)
		}
		if err := netplan.ValidateAddresses(link.Addresses); err != nil {
			return applyError(fmt.Sprintf("validate link %q", link.Name), err)
		}
		if link.Parent != "" {
			if err := netplan.ValidateInterfaceName(link.Parent); err != nil {
				return applyError(fmt.Sprintf("validate parent of link %q", link.Name), err)
			}
		}
		if slices.Contains(link.LinkLocal, "ipv4") {
			return applyError(
				fmt.Sprintf("validate link %q", link.Name),
				errors.New("IPv4 link-local addressing is unsupported"),
			)
		}
		if _, exists := desired[link.Name]; exists {
			return applyError(
				fmt.Sprintf("validate link %q", link.Name),
				errors.New("duplicate desired link name"),
			)
		}
		if link.Parent != "" {
			identity := vlanIdentity{Parent: link.Parent, ID: link.VLANID}
			if previous, duplicate := desiredVLANs[identity]; duplicate {
				return applyError(
					fmt.Sprintf("validate link %q", link.Name),
					fmt.Errorf(
						"VLAN parent %q ID %d is already used by %q",
						link.Parent,
						link.VLANID,
						previous,
					),
				)
			}
			desiredVLANs[identity] = link.Name
		}
		desired[link.Name] = link
	}
	for _, link := range state.Links {
		if link.Parent == "" {
			continue
		}
		parent, found := desired[link.Parent]
		if !found {
			return applyError(
				fmt.Sprintf("validate parent of VLAN %q", link.Name),
				fmt.Errorf("parent %q is missing from desired state", link.Parent),
			)
		}
		if parent.Parent != "" {
			return applyError(
				fmt.Sprintf("validate parent of VLAN %q", link.Name),
				fmt.Errorf("parent %q is not a base link", link.Parent),
			)
		}
		if link.MTU != 0 && parent.MTU != 0 && link.MTU > parent.MTU {
			return applyError(
				fmt.Sprintf("validate MTU of VLAN %q", link.Name),
				fmt.Errorf("VLAN MTU %d exceeds parent %q MTU %d", link.MTU, link.Parent, parent.MTU),
			)
		}
	}

	links, err := m.backend.LinkList()
	if err != nil {
		return applyError("list links", err)
	}
	existing := make(map[string]vnetlink.Link, len(links))
	for idx, link := range links {
		if link == nil || link.Attrs() == nil {
			return applyError(
				"validate link dump",
				fmt.Errorf("link %d is incomplete", idx),
			)
		}
		name := link.Attrs().Name
		if _, duplicate := existing[name]; duplicate {
			return applyError(
				"validate link dump",
				fmt.Errorf("link name %q appears more than once", name),
			)
		}
		existing[name] = link
	}
	m.forgetReplacedLinks(existing)

	// Validate all bases and existing VLANs before changing any link.
	recreate := map[string]struct{}{}
	for _, wanted := range state.Links {
		if wanted.Parent == "" {
			base, exists := existing[wanted.Name]
			if !exists {
				return applyError(
					fmt.Sprintf("find base link %q", wanted.Name),
					errors.New("base KNI link is not available yet"),
				)
			}
			if _, vlan := base.(*vnetlink.Vlan); vlan {
				return applyError(
					fmt.Sprintf("validate base link %q", wanted.Name),
					errors.New("base KNI link is unexpectedly a VLAN"),
				)
			}
			if wanted.MTU == 0 {
				if err := netplan.ValidateMTU(base.Attrs().MTU); err != nil {
					return applyError(fmt.Sprintf("validate effective MTU of link %q", wanted.Name), err)
				}
			}
			continue
		}

		parent, exists := existing[wanted.Parent]
		if !exists {
			return applyError(
				fmt.Sprintf("find parent %q for VLAN %q", wanted.Parent, wanted.Name),
				errors.New("base KNI link is not available yet"),
			)
		}
		if current, exists := existing[wanted.Name]; exists {
			vlan, ok := current.(*vnetlink.Vlan)
			if !ok {
				return applyError(
					fmt.Sprintf("validate existing link %q", wanted.Name),
					fmt.Errorf("link type %q is not vlan", current.Type()),
				)
			}
			if vlan.ParentIndex != parent.Attrs().Index ||
				vlan.VlanId != wanted.VLANID ||
				vlan.VlanProtocol != managedVLANProtocol {
				if current.Attrs().Alias == managedAlias {
					recreate[wanted.Name] = struct{}{}
					continue
				}
				return applyError(
					fmt.Sprintf("validate existing VLAN %q", wanted.Name),
					fmt.Errorf(
						"parent index/ID is %d/%d, want %d/%d; protocol is %s, want %s",
						vlan.ParentIndex,
						vlan.VlanId,
						parent.Attrs().Index,
						wanted.VLANID,
						vlan.VlanProtocol,
						managedVLANProtocol,
					),
				)
			}
			if current.Attrs().Alias != managedAlias {
				ownership := fmt.Sprintf("link alias %q belongs to another owner", current.Attrs().Alias)
				if current.Attrs().Alias == "" {
					ownership = fmt.Sprintf(
						"link requires explicit ownership handoff with alias %q",
						managedAlias,
					)
				}
				return applyError(
					fmt.Sprintf("validate existing VLAN %q", wanted.Name),
					errors.New(ownership),
				)
			}
		}
	}
	creationMTUs := make(map[string]int, len(state.Links))
	for _, child := range state.Links {
		if child.Parent == "" {
			continue
		}
		parentMTU := desired[child.Parent].MTU
		if parentMTU == 0 {
			parentMTU = existing[child.Parent].Attrs().MTU
		}
		effectiveMTU := child.MTU
		if effectiveMTU == 0 {
			if current, exists := existing[child.Name]; exists {
				effectiveMTU = current.Attrs().MTU
			} else {
				effectiveMTU = parentMTU
			}
		}
		if err := netplan.ValidateMTU(effectiveMTU); err != nil {
			return applyError(fmt.Sprintf("validate effective MTU of VLAN %q", child.Name), err)
		}
		creationMTUs[child.Name] = effectiveMTU
		if effectiveMTU != 0 && parentMTU != 0 && effectiveMTU > parentMTU {
			return applyError(
				fmt.Sprintf("validate MTU of VLAN %q", child.Name),
				fmt.Errorf(
					"effective VLAN MTU %d exceeds parent %q MTU %d",
					effectiveMTU,
					child.Parent,
					parentMTU,
				),
			)
		}
	}
	for _, parent := range state.Links {
		if parent.Parent != "" || parent.MTU == 0 {
			continue
		}
		currentParent := existing[parent.Name]
		for _, currentChild := range links {
			if currentChild.Attrs().ParentIndex != currentParent.Attrs().Index {
				continue
			}
			child, configured := desired[currentChild.Attrs().Name]
			if configured && child.Parent == parent.Name {
				continue
			}
			_, managedVLAN := currentChild.(*vnetlink.Vlan)
			if managedVLAN && currentChild.Attrs().Alias == managedAlias {
				continue
			}
			if currentChild.Attrs().MTU > parent.MTU {
				return applyError(
					fmt.Sprintf("validate MTU of child link %q", currentChild.Attrs().Name),
					fmt.Errorf(
						"current MTU %d exceeds desired parent %q MTU %d",
						currentChild.Attrs().MTU,
						parent.Name,
						parent.MTU,
					),
				)
			}
		}
	}

	// Complete every required address dump before the first mutation. This
	// avoids destructive partial applies when a dump is interrupted.
	for _, wanted := range state.Links {
		link, exists := existing[wanted.Name]
		if !exists {
			continue
		}
		if err := checkContext(ctx, fmt.Sprintf("list addresses on link %q", wanted.Name)); err != nil {
			return err
		}
		_, err := m.backend.AddrList(link, vnetlink.FAMILY_ALL)
		if err != nil {
			return applyError(fmt.Sprintf("list addresses on link %q", wanted.Name), err)
		}
	}

	desiredKernelVLANs := make(map[[2]int]string, len(desiredVLANs))
	for identity, name := range desiredVLANs {
		desiredKernelVLANs[[2]int{existing[identity.Parent].Attrs().Index, identity.ID}] = name
	}
	var earlyDeletes []vnetlink.Link
	for _, link := range links {
		vlan, ok := link.(*vnetlink.Vlan)
		if !ok {
			continue
		}
		name := link.Attrs().Name
		target, conflicts := desiredKernelVLANs[[2]int{vlan.ParentIndex, vlan.VlanId}]
		conflicts = conflicts && vlan.VlanProtocol == managedVLANProtocol && target != name
		if conflicts && link.Attrs().Alias != managedAlias {
			return applyError(
				fmt.Sprintf("validate VLAN identity for %q", target),
				fmt.Errorf("identity is occupied by unowned link %q", name),
			)
		}
		_, configured := desired[name]
		_, needsRecreation := recreate[name]
		if link.Attrs().Alias != managedAlias || (configured && !needsRecreation) {
			continue
		}
		if err := validateNoDependentLinks(link, links); err != nil {
			return applyError(fmt.Sprintf("validate deletion of VLAN %q", name), err)
		}
		if conflicts || needsRecreation {
			earlyDeletes = append(earlyDeletes, link)
		}
	}

	if err := m.cleanupStaleOwnedAddresses(ctx, links, desired, existing); err != nil {
		return err
	}

	// Raise parent MTUs before creating or recreating VLANs. Decreases are
	// deferred until child links have reached their desired MTUs.
	for _, wanted := range state.Links {
		if wanted.Parent != "" || wanted.MTU == 0 {
			continue
		}
		link := existing[wanted.Name]
		if link.Attrs().MTU >= wanted.MTU {
			continue
		}
		link, err = m.setConfiguredLinkMTU(ctx, link, wanted, existing)
		if err != nil {
			return err
		}
		existing[wanted.Name] = link
	}

	// Release every occupied replacement identity before creating any VLAN.
	for _, link := range earlyDeletes {
		name := link.Attrs().Name
		if wanted, configured := desired[name]; configured {
			link, err = m.revalidateConfiguredLink(
				ctx,
				link,
				wanted,
				existing,
				fmt.Sprintf("delete owned VLAN %q for recreation", name),
			)
			if err != nil {
				return err
			}
		}
		if err := m.deleteOwnedVLAN(
			ctx,
			link,
			fmt.Sprintf("delete conflicting owned VLAN %q", name),
		); err != nil {
			return err
		}
		delete(existing, name)
	}

	managed := make(map[string]vnetlink.Link, len(state.Links))
	for _, wanted := range state.Links {
		if err := checkContext(ctx, fmt.Sprintf("configure link %q", wanted.Name)); err != nil {
			return err
		}
		link, exists := existing[wanted.Name]
		if !exists {
			parent := existing[wanted.Parent]
			parent, err = m.revalidateLink(
				ctx,
				parent,
				fmt.Sprintf("create VLAN %q", wanted.Name),
			)
			if err != nil {
				return err
			}
			existing[wanted.Parent] = parent
			created := &vnetlink.Vlan{
				LinkAttrs: vnetlink.LinkAttrs{
					Name:        wanted.Name,
					ParentIndex: parent.Attrs().Index,
					MTU:         creationMTUs[wanted.Name],
					Alias:       managedAlias,
				},
				VlanId:       wanted.VLANID,
				VlanProtocol: managedVLANProtocol,
			}
			if err := m.backend.LinkAdd(created); err != nil {
				return applyError(fmt.Sprintf("create VLAN %q", wanted.Name), err)
			}
			link, err = m.backend.LinkByName(wanted.Name)
			if err != nil {
				return applyError(fmt.Sprintf("find newly created VLAN %q", wanted.Name), err)
			}
			if err := validateCreatedVLAN(
				link,
				wanted,
				parent.Attrs().Index,
				creationMTUs[wanted.Name],
			); err != nil {
				return applyError(fmt.Sprintf("validate newly created VLAN %q", wanted.Name), err)
			}
			existing[wanted.Name] = link
		}
		link, err = m.revalidateConfiguredLink(
			ctx,
			link,
			wanted,
			existing,
			fmt.Sprintf("verify link %q", wanted.Name),
		)
		if err != nil {
			return err
		}
		existing[wanted.Name] = link
		managed[wanted.Name] = link
	}

	configurationOrder := make([]netplan.Link, 0, len(state.Links))
	for _, wanted := range state.Links {
		if wanted.Parent == "" {
			configurationOrder = append(configurationOrder, wanted)
		}
	}
	for _, wanted := range state.Links {
		if wanted.Parent != "" {
			configurationOrder = append(configurationOrder, wanted)
		}
	}
	for _, wanted := range configurationOrder {
		link := managed[wanted.Name]
		if wanted.Parent != "" && wanted.MTU != 0 && link.Attrs().MTU != wanted.MTU {
			link, err = m.setConfiguredLinkMTU(ctx, link, wanted, existing)
			if err != nil {
				return err
			}
		}
		addrGenMode := "1"
		if slices.Contains(wanted.LinkLocal, "ipv6") {
			addrGenMode = "0"
		}
		if wanted.AcceptRA != nil {
			acceptRA := "0"
			if *wanted.AcceptRA {
				// Forwarding is enabled on dataplane hosts. Linux value 1 ignores
				// router advertisements while forwarding; value 2 accepts them.
				acceptRA = "2"
			}
			if err := setSysctl(ctx, m.sysctl, wanted.Name, "accept_ra", acceptRA, func() error {
				var validationErr error
				link, validationErr = m.revalidateConfiguredLink(
					ctx,
					link,
					wanted,
					existing,
					fmt.Sprintf("set IPv6 sysctl %q on link %q", "accept_ra", wanted.Name),
				)
				return validationErr
			}); err != nil {
				return err
			}
		}
		if err := setSysctl(ctx, m.sysctl, wanted.Name, "addr_gen_mode", addrGenMode, func() error {
			var validationErr error
			link, validationErr = m.revalidateConfiguredLink(
				ctx,
				link,
				wanted,
				existing,
				fmt.Sprintf("set IPv6 sysctl %q on link %q", "addr_gen_mode", wanted.Name),
			)
			return validationErr
		}); err != nil {
			return err
		}

		if err := checkContext(ctx, fmt.Sprintf("set link %q up", wanted.Name)); err != nil {
			return err
		}
		link, err = m.revalidateConfiguredLink(
			ctx,
			link,
			wanted,
			existing,
			fmt.Sprintf("set link %q up", wanted.Name),
		)
		if err != nil {
			return err
		}
		if err := m.backend.LinkSetUp(link); err != nil {
			return applyError(fmt.Sprintf("set link %q up", wanted.Name), err)
		}
		existing[wanted.Name] = link
		managed[wanted.Name] = link
	}

	addressSnapshots := make(map[string][]vnetlink.Addr, len(managed))
	for _, wanted := range state.Links {
		if err := checkContext(ctx, fmt.Sprintf("list addresses on link %q", wanted.Name)); err != nil {
			return err
		}
		link, err := m.revalidateConfiguredLink(
			ctx,
			managed[wanted.Name],
			wanted,
			existing,
			fmt.Sprintf("list addresses on link %q", wanted.Name),
		)
		if err != nil {
			return err
		}
		managed[wanted.Name] = link
		addresses, err := m.backend.AddrList(link, vnetlink.FAMILY_ALL)
		if err != nil {
			return applyError(fmt.Sprintf("list addresses on link %q", wanted.Name), err)
		}
		addressSnapshots[wanted.Name] = addresses
	}

	for _, wanted := range state.Links {
		link := managed[wanted.Name]
		desiredAddresses := make(map[string]vnetlink.Addr, len(wanted.Addresses))
		addresses := make([]vnetlink.Addr, 0, len(wanted.Addresses))
		for _, prefix := range wanted.Addresses {
			address := addrFromPrefix(prefix)
			desiredAddresses[addrKey(address)] = address
			addresses = append(addresses, address)
		}
		m.forgetMissingAddresses(link, addressSnapshots[wanted.Name])

		// A withdrawn IPv4 primary must not remove desired secondaries after
		// they have been installed. Reinstall owned dependents after deletion.
		for _, address := range addressSnapshots[wanted.Name] {
			key := addrKey(address)
			if _, keep := desiredAddresses[key]; keep || !m.ownsAddress(link, key) {
				continue
			}
			if address.Flags&unix.IFA_F_SECONDARY != 0 {
				continue
			}
			if !slices.ContainsFunc(addresses, func(desired vnetlink.Addr) bool {
				return sameIPv4Subnet(address, desired)
			}) {
				continue
			}
			// A desired secondary must be claimed successfully before primary
			// withdrawal can authorize its cascading deletion and reinstall.
			for _, secondary := range addressSnapshots[wanted.Name] {
				secondaryKey := addrKey(secondary)
				desired, keep := desiredAddresses[secondaryKey]
				if !keep || m.ownsAddress(link, secondaryKey) ||
					secondary.Flags&unix.IFA_F_SECONDARY == 0 || !sameIPv4Subnet(address, secondary) {
					continue
				}
				link, err = m.replaceAddress(ctx, link, wanted, existing, desired)
				if err != nil {
					return err
				}
			}
			link, err = m.deleteAddress(ctx, link, wanted, existing, key, false)
			if err != nil {
				return err
			}
		}

		for _, address := range addresses {
			if address.IP.To4() == nil {
				link, err = m.revalidateConfiguredLink(
					ctx, link, wanted, existing,
					fmt.Sprintf("replace address on link %q", wanted.Name),
				)
				if err != nil {
					return err
				}
				currentAddresses, err := m.backend.AddrList(link, vnetlink.FAMILY_V6)
				if err != nil {
					return applyError(fmt.Sprintf("revalidate IPv6 addresses on link %q", wanted.Name), err)
				}
				for _, current := range currentAddresses {
					if current.IPNet == nil || !current.IP.Equal(address.IP) ||
						addrKey(current) == addrKey(address) {
						continue
					}
					if !m.ownsAddress(link, addrKey(current)) {
						return applyError(
							fmt.Sprintf("replace IPv6 prefix on link %q", wanted.Name),
							fmt.Errorf("unowned IPv6 address %q has a different prefix", addrKey(current)),
						)
					}
					// Linux replaces IPv6 properties without changing the prefix.
					link, err = m.deleteAddress(ctx, link, wanted, existing, addrKey(current), false)
					if err != nil {
						return err
					}
				}
			}
			link, err = m.replaceAddress(ctx, link, wanted, existing, address)
			if err != nil {
				return err
			}
		}

		linkLocalEnabled := slices.Contains(wanted.LinkLocal, "ipv6")
		for _, address := range addressSnapshots[wanted.Name] {
			key := addrKey(address)
			if _, keep := desiredAddresses[key]; keep {
				continue
			}
			if !m.ownsAddress(link, key) && (linkLocalEnabled || !isIPv6LinkLocal(address)) {
				continue
			}
			link, err = m.deleteAddress(ctx, link, wanted, existing, key, !linkLocalEnabled)
			if err != nil {
				return err
			}
		}
	}

	for _, link := range links {
		if err := checkContext(ctx, "delete stale managed VLANs"); err != nil {
			return err
		}
		if _, wanted := desired[link.Attrs().Name]; wanted {
			continue
		}
		if _, exists := existing[link.Attrs().Name]; !exists {
			continue
		}
		if link.Attrs().Alias != managedAlias {
			continue
		}
		if _, vlan := link.(*vnetlink.Vlan); !vlan {
			continue
		}
		if err := m.deleteOwnedVLAN(
			ctx,
			link,
			fmt.Sprintf("delete stale VLAN %q", link.Attrs().Name),
		); err != nil {
			return err
		}
	}

	for _, wanted := range state.Links {
		if wanted.Parent != "" || wanted.MTU == 0 {
			continue
		}
		link := managed[wanted.Name]
		if link.Attrs().MTU == wanted.MTU {
			continue
		}
		if link.Attrs().MTU > wanted.MTU {
			if err := m.validateParentMTUDecrease(ctx, link, wanted.MTU); err != nil {
				return err
			}
		}
		link, err = m.setConfiguredLinkMTU(ctx, link, wanted, existing)
		if err != nil {
			return err
		}
		managed[wanted.Name] = link
	}
	return nil
}

func (m *Reconciler) cleanupStaleOwnedAddresses(
	ctx context.Context,
	links []vnetlink.Link,
	desired map[string]netplan.Link,
	existing map[string]vnetlink.Link,
) error {
	for _, expected := range links {
		name := expected.Attrs().Name
		if _, wanted := desired[name]; wanted {
			continue
		}
		if _, vlan := expected.(*vnetlink.Vlan); vlan && expected.Attrs().Alias == managedAlias {
			continue
		}
		owned, found := m.ownedAddresses[name]
		if !found || owned.Identity != identifyLink(expected) {
			continue
		}

		operation := fmt.Sprintf("delete stale owned addresses on link %q", name)
		current, err := m.revalidateLink(ctx, expected, operation)
		if err != nil {
			return err
		}
		existing[name] = current
		addresses, err := m.backend.AddrList(current, vnetlink.FAMILY_ALL)
		if err != nil {
			return applyError(operation, fmt.Errorf("re-list addresses: %w", err))
		}
		m.forgetMissingAddresses(current, addresses)

		for _, address := range addresses {
			key := addrKey(address)
			if !m.ownsAddress(current, key) {
				continue
			}
			current, err = m.deleteAddress(ctx, current, netplan.Link{Name: name}, existing, key, false)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Reconciler) replaceAddress(
	ctx context.Context,
	expected vnetlink.Link,
	wanted netplan.Link,
	existing map[string]vnetlink.Link,
	address vnetlink.Addr,
) (vnetlink.Link, error) {
	operation := fmt.Sprintf("replace address %q on link %q", addrKey(address), wanted.Name)
	current, err := m.revalidateConfiguredLink(ctx, expected, wanted, existing, operation)
	if err != nil {
		return nil, err
	}
	if err := m.backend.AddrReplace(current, &address); err != nil {
		return nil, applyError(operation, err)
	}
	m.rememberAddress(current, addrKey(address))
	return current, nil
}

func (m *Reconciler) deleteAddress(
	ctx context.Context,
	expected vnetlink.Link,
	wanted netplan.Link,
	existing map[string]vnetlink.Link,
	key string,
	allowLinkLocal bool,
) (vnetlink.Link, error) {
	operation := fmt.Sprintf("delete address %q on link %q", key, wanted.Name)
	current, err := m.revalidateConfiguredLink(ctx, expected, wanted, existing, operation)
	if err != nil {
		return nil, err
	}
	addresses, err := m.backend.AddrList(current, vnetlink.FAMILY_ALL)
	if err != nil {
		return nil, applyError(operation, fmt.Errorf("revalidate addresses: %w", err))
	}
	address, found, err := uniqueAddress(addresses, key)
	if err != nil {
		return nil, applyError(operation, err)
	}
	if !found {
		m.forgetAddress(current, key)
		return current, nil
	}
	current, err = m.revalidateConfiguredLink(ctx, current, wanted, existing, operation)
	if err != nil {
		return nil, err
	}
	if !m.ownsAddress(current, key) && !(allowLinkLocal && isIPv6LinkLocal(address)) {
		return nil, applyError(operation, errors.New("address ownership changed during reconciliation"))
	}
	// Without an explicit IPv4 promotion policy, deleting a primary can
	// also delete its secondaries. Never permit that for unowned addresses.
	if address.Flags&unix.IFA_F_SECONDARY == 0 {
		for _, secondary := range addresses {
			if secondary.Flags&unix.IFA_F_SECONDARY != 0 &&
				sameIPv4Subnet(address, secondary) && !m.ownsAddress(current, addrKey(secondary)) {
				return nil, applyError(
					operation,
					fmt.Errorf("unowned secondary %q prevents primary deletion", addrKey(secondary)),
				)
			}
		}
	}
	if err := checkContext(ctx, operation); err != nil {
		return nil, err
	}
	if err := m.backend.AddrDel(current, &address); err != nil {
		return nil, applyError(operation, err)
	}
	m.forgetAddress(current, key)
	return current, nil
}

func sameIPv4Subnet(first, second vnetlink.Addr) bool {
	if first.IPNet == nil || second.IPNet == nil ||
		first.IP.To4() == nil || second.IP.To4() == nil {
		return false
	}
	firstSubnet, secondSubnet := first.IPNet, second.IPNet
	if first.Peer != nil {
		firstSubnet = first.Peer
	}
	if second.Peer != nil {
		secondSubnet = second.Peer
	}
	return bytes.Equal(firstSubnet.Mask, secondSubnet.Mask) && firstSubnet.Contains(secondSubnet.IP)
}

func (m *Reconciler) validateParentMTUDecrease(
	ctx context.Context,
	expected vnetlink.Link,
	mtu int,
) error {
	operation := fmt.Sprintf("validate child MTUs before lowering link %q", expected.Attrs().Name)
	if err := checkContext(ctx, operation); err != nil {
		return err
	}
	links, err := m.backend.LinkList()
	if err != nil {
		return applyError(operation, err)
	}
	parent, err := m.revalidateLink(ctx, expected, operation)
	if err != nil {
		return err
	}
	for idx, child := range links {
		if child == nil || child.Attrs() == nil {
			return applyError(operation, fmt.Errorf("link %d is incomplete", idx))
		}
		if child.Attrs().ParentIndex == parent.Attrs().Index && child.Attrs().MTU > mtu {
			return applyError(
				operation,
				fmt.Errorf(
					"child link %q MTU %d exceeds desired parent MTU %d",
					child.Attrs().Name,
					child.Attrs().MTU,
					mtu,
				),
			)
		}
	}
	return nil
}

func (m *Reconciler) setConfiguredLinkMTU(
	ctx context.Context,
	expected vnetlink.Link,
	wanted netplan.Link,
	existing map[string]vnetlink.Link,
) (vnetlink.Link, error) {
	operation := fmt.Sprintf("set MTU on link %q", wanted.Name)
	current, err := m.revalidateConfiguredLink(ctx, expected, wanted, existing, operation)
	if err != nil {
		return nil, err
	}
	if err := m.backend.LinkSetMTU(current, wanted.MTU); err != nil {
		return nil, applyError(operation, err)
	}
	current.Attrs().MTU = wanted.MTU
	existing[wanted.Name] = current
	return current, nil
}

func (m *Reconciler) deleteOwnedVLAN(
	ctx context.Context,
	expected vnetlink.Link,
	operation string,
) error {
	// Netlink has no conditional LinkDel operation. The ownership handoff
	// represented by managedAlias includes the whole link and excludes
	// concurrent writers; the identity check below rejects link replacement.
	current, err := m.revalidateLink(ctx, expected, operation)
	if err != nil {
		return err
	}
	if _, vlan := current.(*vnetlink.Vlan); !vlan || current.Attrs().Alias != managedAlias {
		return applyError(operation, errors.New("link is not an owned VLAN"))
	}
	links, err := m.backend.LinkList()
	if err != nil {
		return applyError(operation, err)
	}
	if err := validateNoDependentLinks(current, links); err != nil {
		return applyError(operation, err)
	}
	current, err = m.revalidateLink(ctx, current, operation)
	if err != nil {
		return err
	}
	if err := m.backend.LinkDel(current); err != nil {
		return applyError(operation, err)
	}
	m.forgetLink(current)
	return nil
}

func validateNoDependentLinks(parent vnetlink.Link, links []vnetlink.Link) error {
	for idx, link := range links {
		if link == nil || link.Attrs() == nil {
			return fmt.Errorf("link %d is incomplete", idx)
		}
		if link.Attrs().ParentIndex == parent.Attrs().Index {
			return fmt.Errorf("dependent link %q prevents VLAN deletion", link.Attrs().Name)
		}
	}
	return nil
}

func validateCreatedVLAN(
	link vnetlink.Link,
	wanted netplan.Link,
	parentIndex int,
	expectedMTU int,
) error {
	if link == nil || link.Attrs() == nil {
		return errors.New("created link is incomplete")
	}
	vlan, ok := link.(*vnetlink.Vlan)
	if !ok {
		return fmt.Errorf("created link type %q is not vlan", link.Type())
	}
	if expectedMTU != 0 && link.Attrs().MTU != expectedMTU {
		return fmt.Errorf("created VLAN MTU is %d, want %d", link.Attrs().MTU, expectedMTU)
	}
	if link.Attrs().Name != wanted.Name || link.Attrs().Index <= 0 ||
		link.Attrs().Alias != managedAlias || vlan.ParentIndex != parentIndex ||
		vlan.VlanId != wanted.VLANID || vlan.VlanProtocol != managedVLANProtocol {
		return fmt.Errorf(
			"created VLAN identity is %q/%d/%q/%d/%d/%s, want %q/positive/%q/%d/%d/%s",
			link.Attrs().Name,
			link.Attrs().Index,
			link.Attrs().Alias,
			vlan.ParentIndex,
			vlan.VlanId,
			vlan.VlanProtocol,
			wanted.Name,
			managedAlias,
			parentIndex,
			wanted.VLANID,
			managedVLANProtocol,
		)
	}
	return nil
}

func (m *Reconciler) revalidateConfiguredLink(
	ctx context.Context,
	expected vnetlink.Link,
	wanted netplan.Link,
	expectedLinks map[string]vnetlink.Link,
	operation string,
) (vnetlink.Link, error) {
	if wanted.Parent != "" {
		expectedParent, found := expectedLinks[wanted.Parent]
		if !found {
			return nil, applyError(
				operation,
				fmt.Errorf("expected parent %q is missing", wanted.Parent),
			)
		}
		parent, err := m.revalidateLink(ctx, expectedParent, operation)
		if err != nil {
			return nil, err
		}
		expectedLinks[wanted.Parent] = parent
	}

	current, err := m.revalidateLink(ctx, expected, operation)
	if err != nil {
		return nil, err
	}
	expectedLinks[wanted.Name] = current
	return current, nil
}

func (m *Reconciler) revalidateLink(
	ctx context.Context,
	expected vnetlink.Link,
	operation string,
) (vnetlink.Link, error) {
	if expected == nil || expected.Attrs() == nil {
		return nil, applyError(operation, errors.New("expected link is incomplete"))
	}
	if err := checkContext(ctx, operation); err != nil {
		return nil, err
	}
	current, err := m.backend.LinkByName(expected.Attrs().Name)
	if err != nil {
		return nil, applyError(operation, fmt.Errorf("re-resolve link: %w", err))
	}
	if err := validateLinkIdentity(expected, current); err != nil {
		return nil, applyError(operation, err)
	}
	if err := checkContext(ctx, operation); err != nil {
		return nil, err
	}
	return current, nil
}

func validateLinkIdentity(expected, current vnetlink.Link) error {
	if current == nil || current.Attrs() == nil {
		return errors.New("re-resolved link is incomplete")
	}
	if identifyLink(expected) != identifyLink(current) {
		return fmt.Errorf("link %q identity changed during reconciliation", expected.Attrs().Name)
	}
	return nil
}

func identifyLink(link vnetlink.Link) linkIdentity {
	attributes := link.Attrs()
	identity := linkIdentity{
		Name:            attributes.Name,
		Index:           attributes.Index,
		Type:            link.Type(),
		Alias:           attributes.Alias,
		ParentIndex:     attributes.ParentIndex,
		HardwareAddress: string(attributes.HardwareAddr),
	}
	if vlan, ok := link.(*vnetlink.Vlan); ok {
		identity.IsVLAN = true
		identity.VLANID = vlan.VlanId
		identity.VLANProtocol = vlan.VlanProtocol
	}
	return identity
}

func uniqueAddress(addresses []vnetlink.Addr, key string) (vnetlink.Addr, bool, error) {
	var match vnetlink.Addr
	found := false
	for _, address := range addresses {
		if addrKey(address) != key {
			continue
		}
		if found {
			return vnetlink.Addr{}, false, fmt.Errorf("address %q appears more than once", key)
		}
		match = address
		found = true
	}
	return match, found, nil
}

func (m *Reconciler) forgetReplacedLinks(existing map[string]vnetlink.Link) {
	for name, owned := range m.ownedAddresses {
		link, exists := existing[name]
		if !exists || identifyLink(link) != owned.Identity {
			delete(m.ownedAddresses, name)
		}
	}
}

func (m *Reconciler) forgetMissingAddresses(link vnetlink.Link, addresses []vnetlink.Addr) {
	owned, exists := m.ownedAddresses[link.Attrs().Name]
	if !exists || owned.Identity != identifyLink(link) {
		return
	}

	present := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		present[addrKey(address)] = struct{}{}
	}
	for key := range owned.Addresses {
		if _, exists := present[key]; !exists {
			delete(owned.Addresses, key)
		}
	}
	if len(owned.Addresses) == 0 {
		delete(m.ownedAddresses, link.Attrs().Name)
	}
}

func (m *Reconciler) rememberAddress(link vnetlink.Link, key string) {
	owned, exists := m.ownedAddresses[link.Attrs().Name]
	if !exists || owned.Identity != identifyLink(link) {
		owned = ownedLinkAddresses{
			Identity:  identifyLink(link),
			Addresses: map[string]struct{}{},
		}
	}
	owned.Addresses[key] = struct{}{}
	m.ownedAddresses[link.Attrs().Name] = owned
}

func (m *Reconciler) ownsAddress(link vnetlink.Link, key string) bool {
	owned, exists := m.ownedAddresses[link.Attrs().Name]
	if !exists || owned.Identity != identifyLink(link) {
		return false
	}
	_, exists = owned.Addresses[key]
	return exists
}

func (m *Reconciler) forgetAddress(link vnetlink.Link, key string) {
	owned, exists := m.ownedAddresses[link.Attrs().Name]
	if !exists || owned.Identity != identifyLink(link) {
		return
	}
	delete(owned.Addresses, key)
	if len(owned.Addresses) == 0 {
		delete(m.ownedAddresses, link.Attrs().Name)
	}
}

func (m *Reconciler) forgetLink(link vnetlink.Link) {
	owned, exists := m.ownedAddresses[link.Attrs().Name]
	if exists && owned.Identity == identifyLink(link) {
		delete(m.ownedAddresses, link.Attrs().Name)
	}
}

func setSysctl(
	ctx context.Context,
	sysctl Sysctl,
	name string,
	setting string,
	value string,
	validate func() error,
) error {
	if err := checkContext(ctx, fmt.Sprintf("set IPv6 sysctl %q on link %q", setting, name)); err != nil {
		return err
	}
	if err := sysctl.SetIPv6(ctx, name, setting, value, validate); err != nil {
		return applyError(fmt.Sprintf("set IPv6 sysctl %q on link %q", setting, name), err)
	}
	return nil
}

func addrFromPrefix(prefix netip.Prefix) vnetlink.Addr {
	bits := 128
	ip := net.IP(prefix.Addr().AsSlice())
	if prefix.Addr().Is4() {
		bits = 32
	}
	return vnetlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: net.CIDRMask(prefix.Bits(), bits)}}
}

func addrKey(address vnetlink.Addr) string {
	if address.IPNet == nil {
		return "<nil>"
	}
	ones, _ := address.IPNet.Mask.Size()
	return address.IPNet.IP.String() + "/" + fmt.Sprint(ones)
}

func isIPv6LinkLocal(address vnetlink.Addr) bool {
	if address.IPNet == nil || address.IPNet.IP.To4() != nil {
		return false
	}
	return address.IPNet.IP.IsLinkLocalUnicast()
}

func checkContext(ctx context.Context, operation string) error {
	if err := ctx.Err(); err != nil {
		return applyError(operation, err)
	}
	return nil
}

func applyError(operation string, err error) error {
	return fmt.Errorf("apply network state: %s: %w", operation, err)
}

var _ Backend = (*vnetlink.Handle)(nil)
