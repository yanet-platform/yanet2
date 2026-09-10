package neighbour

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"sort"

	vnetlink "github.com/vishvananda/netlink"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// Backend is the netlink surface needed to discover kernel neighbours.
type Backend interface {
	LinkList() ([]vnetlink.Link, error)
	WalkNeighbours(context.Context, func(vnetlink.Neigh) error) error
}

// Entry is a discovered neighbour ready for gateway publication.
type Entry struct {
	NextHop       netip.Addr
	HardwareRoute hwroute.HardwareRoute
	Ifindex       uint32
}

type linkRoute struct {
	netreconcile.LinkIdentity
	SourceMAC [6]byte
}

// Discover returns a complete, deterministically ordered kernel snapshot.
//
// Entries outside the managed netplan links, entries in unusable NUD states,
// and entries without usable IP or EUI-48 addresses are omitted. Any backend
// error invalidates the whole dump.
func Discover(
	ctx context.Context,
	backend Backend,
	state netplan.State,
	linkMap map[string]string,
) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	linksByIndex, err := indexManagedLinks(ctx, links, managedLinks)
	if err != nil {
		return nil, err
	}
	type entryKey struct {
		NextHop netip.Addr
		Device  string
	}
	seen := map[entryKey]Entry{}
	entries := []Entry{}
	count := 0
	err = backend.WalkNeighbours(ctx, func(kernelNeighbour vnetlink.Neigh) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > operatorpb.NeighbourSnapshotEntries {
			return errors.New("dump exceeds entry limit")
		}
		link, managed := linksByIndex[kernelNeighbour.LinkIndex]
		if !managed {
			return nil
		}
		if !usableNeighbourState(kernelNeighbour.State) {
			return nil
		}

		nextHop, valid := netip.AddrFromSlice(kernelNeighbour.IP)
		if !valid {
			return nil
		}
		destinationMAC, usable := hwroute.ParseMAC(kernelNeighbour.HardwareAddr)
		if !usable {
			return nil
		}

		entry := Entry{
			NextHop: nextHop.Unmap(),
			Ifindex: uint32(kernelNeighbour.LinkIndex),
			HardwareRoute: hwroute.HardwareRoute{
				SourceMAC:      link.SourceMAC,
				DestinationMAC: destinationMAC,
				Device:         devicesByLink[link.Name],
			},
		}

		key := entryKey{NextHop: entry.NextHop, Device: entry.HardwareRoute.Device}
		if previous, found := seen[key]; found {
			if previous != entry {
				return fmt.Errorf("conflicting next hop/device %s/%s", key.NextHop, key.Device)
			}
			return nil
		}
		seen[key] = entry
		entries = append(entries, entry)
		return nil
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("discover neighbours: list neighbours: %w", err)
	}
	revalidatedLinks, err := backend.LinkList()
	if err != nil {
		return nil, fmt.Errorf("discover neighbours: revalidate links: %w", err)
	}
	revalidatedByIndex, err := indexManagedLinks(ctx, revalidatedLinks, managedLinks)
	if err != nil {
		return nil, err
	}
	if !maps.Equal(linksByIndex, revalidatedByIndex) {
		return nil, errors.New("discover neighbours: managed links changed during dump")
	}

	sort.Slice(entries, func(first, second int) bool {
		left, right := entries[first], entries[second]
		if comparison := left.NextHop.Compare(right.NextHop); comparison != 0 {
			return comparison < 0
		}
		if left.HardwareRoute.Device != right.HardwareRoute.Device {
			return left.HardwareRoute.Device < right.HardwareRoute.Device
		}
		return false
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func managedLinkConfiguration(
	state netplan.State,
	linkMap map[string]string,
) (map[string]netplan.Link, map[string]string, error) {
	managedLinks := make(map[string]netplan.Link, len(state.Links))
	devicesByLink := make(map[string]string, len(state.Links))
	linksByDevice := make(map[string]string, len(state.Links))
	loopbacks := map[string]bool{}
	for _, link := range state.Links {
		if !link.IsEgress() {
			loopbacks[link.Name] = true
			continue
		}
		if _, duplicate := managedLinks[link.Name]; duplicate {
			return nil, nil, fmt.Errorf("managed link %q is configured more than once", link.Name)
		}
		managedLinks[link.Name] = link

		device := link.Name
		if mapped, found := linkMap[link.Name]; found {
			device = mapped
		}
		if err := operatorpb.ValidateNeighbourDevice(device); err != nil {
			return nil, nil, fmt.Errorf("managed link %q: %w", link.Name, err)
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
		if loopbacks[linkName] {
			continue
		}
		if _, managed := managedLinks[linkName]; !managed {
			return nil, nil, fmt.Errorf("link_map entry %q is not a managed netplan link", linkName)
		}
	}
	return managedLinks, devicesByLink, nil
}

func indexManagedLinks(
	ctx context.Context,
	links []vnetlink.Link,
	managedLinks map[string]netplan.Link,
) (map[int]linkRoute, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	linksByIndex := map[int]linkRoute{}
	linksByName := map[string]vnetlink.Link{}
	for idx, link := range links {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
		sourceMAC, usable := hwroute.ParseMAC(attributes.HardwareAddr)
		if !usable {
			return nil, fmt.Errorf(
				"discover neighbours: managed link %q has unusable hardware address",
				attributes.Name,
			)
		}

		if err := netreconcile.ValidateLink(wanted, link, linksByName[wanted.Parent]); err != nil {
			return nil, fmt.Errorf("discover neighbours: %w", err)
		}
		identity, err := netreconcile.IdentifyLink(link)
		if err != nil {
			return nil, err
		}
		candidate := linkRoute{LinkIdentity: identity, SourceMAC: sourceMAC}
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
