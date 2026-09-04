package neighbour

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"

	vnetlink "github.com/vishvananda/netlink"

	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Backend is the netlink surface needed to discover kernel neighbours.
type Backend interface {
	LinkList() ([]vnetlink.Link, error)
	NeighList(linkIndex, family int) ([]vnetlink.Neigh, error)
}

// NeighbourState is the kernel neighbour-unreachability-detection state.
type NeighbourState int

// HardwareRoute identifies the Layer 2 route used to reach a next hop.
type HardwareRoute struct {
	SourceMAC      [6]byte
	DestinationMAC [6]byte
	Device         string
}

// Entry is a discovered neighbour ready for gateway publication.
type Entry struct {
	NextHop       netip.Addr
	HardwareRoute HardwareRoute
	State         NeighbourState
}

type linkRoute struct {
	Name         string
	SourceMAC    [6]byte
	LinkType     string
	Alias        string
	ParentIndex  int
	VLANID       int
	VLANProtocol vnetlink.VlanProtocol
}

// Discover returns a complete, deterministically ordered kernel snapshot.
//
// Entries outside the managed netplan links, entries in unusable NUD states,
// and entries without usable IP or EUI-48 addresses are omitted. Any backend
// error invalidates the whole dump.
func Discover(
	backend Backend,
	state netplan.State,
	linkMap map[string]string,
) ([]Entry, error) {
	if backend == nil {
		return nil, fmt.Errorf("discover neighbours: netlink backend is nil")
	}

	managedLinks, devicesByLink, err := managedLinkConfiguration(state, linkMap)
	if err != nil {
		return nil, fmt.Errorf("discover neighbours: %w", err)
	}

	links, err := backend.LinkList()
	if err != nil {
		return nil, fmt.Errorf("discover neighbours: list links: %w", err)
	}
	linksByIndex, err := indexManagedLinks(links, managedLinks)
	if err != nil {
		return nil, err
	}
	neighbours, err := backend.NeighList(0, vnetlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("discover neighbours: list neighbours: %w", err)
	}
	revalidatedLinks, err := backend.LinkList()
	if err != nil {
		return nil, fmt.Errorf("discover neighbours: revalidate links: %w", err)
	}
	revalidatedByIndex, err := indexManagedLinks(revalidatedLinks, managedLinks)
	if err != nil {
		return nil, err
	}
	if !sameLinkRoutes(linksByIndex, revalidatedByIndex) {
		return nil, errors.New("discover neighbours: managed links changed during dump")
	}

	type entryKey struct {
		nextHop       netip.Addr
		hardwareRoute HardwareRoute
		state         NeighbourState
	}
	seen := map[entryKey]struct{}{}
	entries := make([]Entry, 0, len(neighbours))
	for _, kernelNeighbour := range neighbours {
		link, managed := linksByIndex[kernelNeighbour.LinkIndex]
		if !managed {
			continue
		}
		if !usableNeighbourState(kernelNeighbour.State) {
			continue
		}

		nextHop, valid := netip.AddrFromSlice(kernelNeighbour.IP)
		if !valid {
			continue
		}
		destinationMAC, usable := usableEUI48(kernelNeighbour.HardwareAddr)
		if !usable {
			continue
		}

		entry := Entry{
			NextHop: nextHop,
			HardwareRoute: HardwareRoute{
				SourceMAC:      link.SourceMAC,
				DestinationMAC: destinationMAC,
				Device:         devicesByLink[link.Name],
			},
			State: NeighbourState(kernelNeighbour.State),
		}

		key := entryKey{nextHop: nextHop, hardwareRoute: entry.HardwareRoute, state: entry.State}
		if _, found := seen[key]; found {
			continue
		}
		seen[key] = struct{}{}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(first, second int) bool {
		left, right := entries[first], entries[second]
		if comparison := left.NextHop.Compare(right.NextHop); comparison != 0 {
			return comparison < 0
		}
		if left.HardwareRoute.Device != right.HardwareRoute.Device {
			return left.HardwareRoute.Device < right.HardwareRoute.Device
		}
		if comparison := bytes.Compare(
			left.HardwareRoute.SourceMAC[:],
			right.HardwareRoute.SourceMAC[:],
		); comparison != 0 {
			return comparison < 0
		}
		if comparison := bytes.Compare(
			left.HardwareRoute.DestinationMAC[:],
			right.HardwareRoute.DestinationMAC[:],
		); comparison != 0 {
			return comparison < 0
		}
		return left.State < right.State
	})
	return entries, nil
}

func managedLinkConfiguration(
	state netplan.State,
	linkMap map[string]string,
) (map[string]netplan.Link, map[string]string, error) {
	managedLinks := make(map[string]netplan.Link, len(state.Links))
	devicesByLink := make(map[string]string, len(state.Links))
	linksByDevice := make(map[string]string, len(state.Links))
	for _, link := range state.Links {
		if _, duplicate := managedLinks[link.Name]; duplicate {
			return nil, nil, fmt.Errorf("managed link %q is configured more than once", link.Name)
		}
		managedLinks[link.Name] = link

		device := link.Name
		if mapped, found := linkMap[link.Name]; found {
			device = mapped
		}
		if device == "" {
			return nil, nil, fmt.Errorf("managed link %q maps to an empty logical device", link.Name)
		}
		if previous, duplicate := linksByDevice[device]; duplicate {
			return nil, nil, fmt.Errorf(
				"managed links %q and %q map to duplicate logical device %q",
				previous,
				link.Name,
				device,
			)
		}
		linksByDevice[device] = link.Name
		devicesByLink[link.Name] = device
	}
	mappedLinks := make([]string, 0, len(linkMap))
	for linkName := range linkMap {
		mappedLinks = append(mappedLinks, linkName)
	}
	sort.Strings(mappedLinks)
	for _, linkName := range mappedLinks {
		if _, managed := managedLinks[linkName]; !managed {
			return nil, nil, fmt.Errorf("link_map entry %q is not a managed netplan link", linkName)
		}
	}
	return managedLinks, devicesByLink, nil
}

func indexManagedLinks(
	links []vnetlink.Link,
	managedLinks map[string]netplan.Link,
) (map[int]linkRoute, error) {
	linksByIndex := map[int]linkRoute{}
	linksByName := map[string]vnetlink.Link{}
	for idx, link := range links {
		if link == nil || link.Attrs() == nil {
			return nil, fmt.Errorf("discover neighbours: link %d is incomplete", idx)
		}
		attributes := link.Attrs()
		if _, duplicate := linksByName[attributes.Name]; duplicate {
			return nil, fmt.Errorf(
				"discover neighbours: link %q appears more than once",
				attributes.Name,
			)
		}
		linksByName[attributes.Name] = link
	}

	managedNames := make([]string, 0, len(managedLinks))
	for name := range managedLinks {
		managedNames = append(managedNames, name)
	}
	sort.Strings(managedNames)
	for _, name := range managedNames {
		wanted := managedLinks[name]
		link, found := linksByName[name]
		if !found {
			return nil, fmt.Errorf("discover neighbours: managed link %q is missing", name)
		}
		attributes := link.Attrs()
		if attributes.Index <= 0 {
			return nil, fmt.Errorf(
				"discover neighbours: managed link %q has invalid index %d",
				attributes.Name,
				attributes.Index,
			)
		}
		sourceMAC, usable := usableEUI48(attributes.HardwareAddr)
		if !usable {
			return nil, fmt.Errorf(
				"discover neighbours: managed link %q has unusable hardware address",
				attributes.Name,
			)
		}

		candidate := linkRoute{
			Name:        attributes.Name,
			SourceMAC:   sourceMAC,
			LinkType:    link.Type(),
			Alias:       attributes.Alias,
			ParentIndex: attributes.ParentIndex,
		}
		if wanted.Parent == "" {
			if _, vlan := link.(*vnetlink.Vlan); vlan {
				return nil, fmt.Errorf(
					"discover neighbours: managed base link %q is unexpectedly a VLAN",
					attributes.Name,
				)
			}
		} else {
			vlan, ok := link.(*vnetlink.Vlan)
			if !ok {
				return nil, fmt.Errorf(
					"discover neighbours: managed link %q is not a VLAN",
					attributes.Name,
				)
			}
			if attributes.Alias != netreconcile.ManagedAlias {
				return nil, fmt.Errorf(
					"discover neighbours: managed VLAN %q has ownership alias %q",
					attributes.Name,
					attributes.Alias,
				)
			}
			if vlan.VlanId != wanted.VLANID {
				return nil, fmt.Errorf(
					"discover neighbours: managed VLAN %q has ID %d, want %d",
					attributes.Name,
					vlan.VlanId,
					wanted.VLANID,
				)
			}
			if vlan.VlanProtocol != vnetlink.VLAN_PROTOCOL_8021Q {
				return nil, fmt.Errorf(
					"discover neighbours: managed VLAN %q has protocol %s, want %s",
					attributes.Name,
					vlan.VlanProtocol,
					vnetlink.VLAN_PROTOCOL_8021Q,
				)
			}
			parent, found := linksByName[wanted.Parent]
			if !found || parent.Attrs() == nil || vlan.ParentIndex != parent.Attrs().Index {
				return nil, fmt.Errorf(
					"discover neighbours: managed VLAN %q has invalid parent %q",
					attributes.Name,
					wanted.Parent,
				)
			}
			candidate.VLANID = vlan.VlanId
			candidate.VLANProtocol = vlan.VlanProtocol
		}
		if existing, found := linksByIndex[attributes.Index]; found {
			return nil, fmt.Errorf(
				"discover neighbours: managed links %q and %q share index %d",
				existing.Name,
				attributes.Name,
				attributes.Index,
			)
		}
		linksByIndex[attributes.Index] = candidate
	}
	return linksByIndex, nil
}

func sameLinkRoutes(first, second map[int]linkRoute) bool {
	if len(first) != len(second) {
		return false
	}
	for index, route := range first {
		if second[index] != route {
			return false
		}
	}
	return true
}

func usableNeighbourState(state int) bool {
	switch state {
	case vnetlink.NUD_REACHABLE,
		vnetlink.NUD_STALE,
		vnetlink.NUD_DELAY,
		vnetlink.NUD_PROBE,
		vnetlink.NUD_NOARP,
		vnetlink.NUD_PERMANENT:
		return true
	default:
		return false
	}
}

func usableEUI48(address net.HardwareAddr) ([6]byte, bool) {
	if len(address) != 6 {
		return [6]byte{}, false
	}

	result := [6]byte(address)
	for _, octet := range result {
		if octet != 0 {
			return result, true
		}
	}
	return [6]byte{}, false
}
