package route

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sort"

	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Backend is the netlink surface needed to reconcile static routes.
type Backend interface {
	LinkByName(string) (vnetlink.Link, error)
	LinkByIndex(int) (vnetlink.Link, error)
	RouteListFiltered(int, *vnetlink.Route, uint64) ([]vnetlink.Route, error)
	RouteAdd(*vnetlink.Route) error
	RouteDel(*vnetlink.Route) error
}

// ReconcilerConfig identifies the route table, protocol, and priority owner.
type ReconcilerConfig struct {
	Table    int
	Protocol int
	Priority int
}

// MaxKernelRouteValue is the largest table or priority value Linux netlink
// can represent without truncation.
const MaxKernelRouteValue = uint64(^uint32(0))

const (
	kernelManagedRouteFlags = unix.RTM_F_CLONED |
		unix.RTM_F_OFFLOAD |
		unix.RTM_F_TRAP |
		unix.RTM_F_OFFLOAD_FAILED
	kernelManagedNexthopFlags = unix.RTNH_F_DEAD |
		unix.RTNH_F_OFFLOAD |
		unix.RTNH_F_LINKDOWN |
		unix.RTNH_F_UNRESOLVED |
		unix.RTNH_F_TRAP
)

// Reconciler applies complete static-route snapshots to one Linux route table.
type Reconciler struct {
	backend  Backend
	table    int
	protocol vnetlink.RouteProtocol
	priority int
}

// NewReconciler creates a reconciler with an exclusive route ownership tag.
func NewReconciler(backend Backend, config ReconcilerConfig) (*Reconciler, error) {
	if backend == nil {
		return nil, errors.New("create route reconciler: netlink backend is nil")
	}
	if config.Table <= 0 {
		return nil, fmt.Errorf(
			"create route reconciler: table must be positive, got %d",
			config.Table,
		)
	}
	if uint64(config.Table) > MaxKernelRouteValue {
		return nil, fmt.Errorf(
			"create route reconciler: table must be within 1..%d, got %d",
			MaxKernelRouteValue,
			config.Table,
		)
	}
	if config.Protocol < 0 || config.Protocol > 255 {
		return nil, fmt.Errorf(
			"create route reconciler: protocol must be within 0..255, got %d",
			config.Protocol,
		)
	}
	if IsSharedRouteProtocol(config.Protocol) {
		return nil, fmt.Errorf(
			"create route reconciler: protocol %d is reserved for a shared route origin",
			config.Protocol,
		)
	}
	if config.Priority <= 0 {
		return nil, fmt.Errorf(
			"create route reconciler: priority must be positive, got %d",
			config.Priority,
		)
	}
	if uint64(config.Priority) > MaxKernelRouteValue {
		return nil, fmt.Errorf(
			"create route reconciler: priority must be within 1..%d, got %d",
			MaxKernelRouteValue,
			config.Priority,
		)
	}
	return &Reconciler{
		backend:  backend,
		table:    config.Table,
		protocol: vnetlink.RouteProtocol(config.Protocol),
		priority: config.Priority,
	}, nil
}

// IsSharedRouteProtocol reports whether protocol identifies a standard route
// origin that the sidecar must not claim as its ownership marker.
func IsSharedRouteProtocol(protocol int) bool {
	switch protocol {
	case unix.RTPROT_UNSPEC,
		unix.RTPROT_REDIRECT,
		unix.RTPROT_KERNEL,
		unix.RTPROT_BOOT,
		unix.RTPROT_STATIC,
		unix.RTPROT_GATED,
		unix.RTPROT_RA,
		unix.RTPROT_MRT,
		unix.RTPROT_ZEBRA,
		unix.RTPROT_BIRD,
		unix.RTPROT_DNROUTED,
		unix.RTPROT_XORP,
		unix.RTPROT_NTK,
		unix.RTPROT_DHCP,
		unix.RTPROT_MROUTED,
		unix.RTPROT_KEEPALIVED,
		unix.RTPROT_BABEL,
		unix.RTPROT_OVN,
		unix.RTPROT_OPENR,
		unix.RTPROT_BGP,
		unix.RTPROT_ISIS,
		unix.RTPROT_OSPF,
		unix.RTPROT_RIP,
		unix.RTPROT_EIGRP:
		return true
	default:
		return false
	}
}

// Apply reconciles desired routes while preserving every foreign route.
//
// Both kernel snapshots and all collision checks complete before the first
// mutation. Stale owned routes are deleted only after every update succeeds.
func (m *Reconciler) Apply(
	ctx context.Context,
	routes []Route,
	state netplan.State,
) error {
	if err := checkContext(ctx, "start reconciliation"); err != nil {
		return err
	}
	if err := validateRoutes(routes); err != nil {
		return fmt.Errorf("reconcile static routes: validate desired routes: %w", err)
	}

	managedInterfaces := map[string]struct{}{}
	for _, link := range state.Links {
		managedInterfaces[link.Name] = struct{}{}
	}
	usedInterfaces := map[string]struct{}{}
	for idx, route := range routes {
		if _, managed := managedInterfaces[route.Interface]; !managed {
			return fmt.Errorf(
				"reconcile static routes: route %d interface %q is not managed by netplan",
				idx,
				route.Interface,
			)
		}
		usedInterfaces[route.Interface] = struct{}{}
	}

	interfaceNames := make([]string, 0, len(usedInterfaces))
	for name := range usedInterfaces {
		interfaceNames = append(interfaceNames, name)
	}
	sort.Strings(interfaceNames)
	linkIndexes := map[string]int{}
	resolvedLinks := map[string]vnetlink.Link{}
	for _, name := range interfaceNames {
		if err := checkContext(ctx, fmt.Sprintf("resolve interface %q", name)); err != nil {
			return err
		}
		link, err := m.backend.LinkByName(name)
		if err != nil {
			return fmt.Errorf(
				"reconcile static routes: resolve interface %q: %w",
				name,
				err,
			)
		}
		if link == nil || link.Attrs() == nil {
			return fmt.Errorf(
				"reconcile static routes: resolve interface %q: incomplete link",
				name,
			)
		}
		if link.Attrs().Name != name {
			return fmt.Errorf(
				"reconcile static routes: resolve interface %q: backend returned %q",
				name,
				link.Attrs().Name,
			)
		}
		if link.Attrs().Index <= 0 {
			return fmt.Errorf(
				"reconcile static routes: resolve interface %q: invalid link index %d",
				name,
				link.Attrs().Index,
			)
		}
		linkIndexes[name] = link.Attrs().Index
		resolvedLinks[name] = link
	}

	desired := buildDesiredRoutes(
		routes,
		linkIndexes,
		m.table,
		m.protocol,
		m.priority,
	)
	desiredPrefixes := map[netip.Prefix]struct{}{}
	for _, route := range desired {
		desiredPrefixes[route.Prefix] = struct{}{}
	}

	if err := checkContext(ctx, "list all routes"); err != nil {
		return err
	}
	allRoutes, err := m.backend.RouteListFiltered(
		vnetlink.FAMILY_ALL,
		&vnetlink.Route{Table: m.table},
		vnetlink.RT_FILTER_TABLE,
	)
	if err != nil {
		return fmt.Errorf(
			"reconcile static routes: list all routes in table %d: %w",
			m.table,
			err,
		)
	}
	if err := checkContext(ctx, "list owned routes"); err != nil {
		return err
	}
	ownedRoutes, err := m.backend.RouteListFiltered(
		vnetlink.FAMILY_ALL,
		&vnetlink.Route{Table: m.table, Protocol: m.protocol},
		vnetlink.RT_FILTER_TABLE|vnetlink.RT_FILTER_PROTOCOL,
	)
	if err != nil {
		return fmt.Errorf(
			"reconcile static routes: list routes in table %d with protocol %d: %w",
			m.table,
			m.protocol,
			err,
		)
	}
	if err := checkContext(ctx, "validate route dumps"); err != nil {
		return err
	}

	for idx, kernelRoute := range allRoutes {
		prefix, isIPRoute, convertErr := prefixFromKernelRoute(kernelRoute)
		if convertErr != nil {
			return fmt.Errorf(
				"reconcile static routes: convert table route %d: %w",
				idx,
				convertErr,
			)
		}
		if !isIPRoute {
			continue
		}
		if _, wanted := desiredPrefixes[prefix]; !wanted {
			continue
		}
		if kernelRoute.Table != m.table || kernelRoute.Priority != m.priority {
			continue
		}
		if !m.owns(kernelRoute) {
			return fmt.Errorf(
				"reconcile static routes: destination %q at priority %d collides with foreign route protocol %d",
				prefix,
				m.priority,
				kernelRoute.Protocol,
			)
		}
	}

	type staleRoute struct {
		Prefix netip.Prefix
		Route  vnetlink.Route
	}
	stale := []staleRoute{}
	currentRoutes := map[netip.Prefix][]vnetlink.Route{}
	for idx, kernelRoute := range ownedRoutes {
		if !m.owns(kernelRoute) {
			continue
		}
		prefix, isIPRoute, convertErr := prefixFromKernelRoute(kernelRoute)
		if convertErr != nil {
			return fmt.Errorf(
				"reconcile static routes: convert owned route %d: %w",
				idx,
				convertErr,
			)
		}
		if !isIPRoute {
			continue
		}
		if _, wanted := desiredPrefixes[prefix]; wanted {
			currentRoutes[prefix] = append(currentRoutes[prefix], kernelRoute)
			continue
		}
		stale = append(stale, staleRoute{Prefix: prefix, Route: kernelRoute})
	}
	sort.SliceStable(stale, func(first, second int) bool {
		return comparePrefixes(stale[first].Prefix, stale[second].Prefix) < 0
	})

	rollbackOperations := []rollbackOperation{}
	for idx := range desired {
		current := currentRoutes[desired[idx].Prefix]
		if err := checkContext(ctx, fmt.Sprintf("inspect destination %q", desired[idx].Prefix)); err != nil {
			return m.rollbackOperations(rollbackOperations, err)
		}
		if len(current) == 1 && routesEquivalent(current[0], desired[idx].Route) {
			continue
		}
		if err := m.revalidateInterfaces(ctx, desired[idx].Interfaces, resolvedLinks); err != nil {
			return m.rollbackOperations(rollbackOperations, fmt.Errorf(
				"reconcile static routes: revalidate destination %q interfaces: %w",
				desired[idx].Prefix,
				err,
			))
		}
		operation := fmt.Sprintf("add destination %q", desired[idx].Prefix)
		if len(current) > 0 {
			operation = fmt.Sprintf("change destination %q", desired[idx].Prefix)
		}
		if err := checkContext(ctx, operation); err != nil {
			return m.rollbackOperations(rollbackOperations, err)
		}
		rollbackCandidates := make([]rollbackRoute, len(current))
		for currentIdx := range current {
			deleteRoute := routeForDelete(current[currentIdx], desired[idx].Prefix)
			rollback, err := m.captureRollbackRoute(deleteRoute)
			if err != nil {
				return m.rollbackOperations(rollbackOperations, fmt.Errorf(
					"reconcile static routes: capture destination %q rollback state: %w",
					desired[idx].Prefix,
					err,
				))
			}
			rollbackCandidates[currentIdx] = rollback
		}
		if err := checkContext(ctx, operation); err != nil {
			return m.rollbackOperations(rollbackOperations, err)
		}
		for currentIdx := range rollbackCandidates {
			if err := checkContext(ctx, operation); err != nil {
				return m.rollbackOperations(rollbackOperations, err)
			}
			deleteRoute := rollbackCandidates[currentIdx].Route
			if err := m.backend.RouteDel(&deleteRoute); err != nil {
				return m.rollbackOperations(rollbackOperations, fmt.Errorf(
					"reconcile static routes: delete changed destination %q: %w",
					desired[idx].Prefix,
					err,
				))
			}
			rollbackOperations = append(rollbackOperations, rollbackOperation{
				Route: rollbackCandidates[currentIdx],
			})
		}

		if len(current) > 0 {
			if err := m.revalidateInterfaces(ctx, desired[idx].Interfaces, resolvedLinks); err != nil {
				return m.rollbackOperations(rollbackOperations, fmt.Errorf(
					"reconcile static routes: revalidate destination %q interfaces after deletion: %w",
					desired[idx].Prefix,
					err,
				))
			}
		}
		if err := m.backend.RouteAdd(&desired[idx].Route); err != nil {
			return m.rollbackOperations(rollbackOperations, fmt.Errorf(
				"reconcile static routes: add destination %q: %w",
				desired[idx].Prefix,
				err,
			))
		}
		rollbackOperations = append(rollbackOperations, rollbackOperation{
			Route:  rollbackRouteForDesired(desired[idx], resolvedLinks),
			Remove: true,
		})
		if err := checkContext(
			ctx,
			fmt.Sprintf("finish destination %q", desired[idx].Prefix),
		); err != nil {
			return m.rollbackOperations(rollbackOperations, err)
		}
	}

	for idx := range stale {
		if err := checkContext(
			ctx,
			fmt.Sprintf("delete stale destination %q", stale[idx].Prefix),
		); err != nil {
			return m.rollbackOperations(rollbackOperations, err)
		}
		deleteRoute := routeForDelete(stale[idx].Route, stale[idx].Prefix)
		rollback, err := m.captureRollbackRoute(deleteRoute)
		if err != nil {
			return m.rollbackOperations(rollbackOperations, fmt.Errorf(
				"reconcile static routes: capture stale destination %q rollback state: %w",
				stale[idx].Prefix,
				err,
			))
		}
		if err := checkContext(
			ctx,
			fmt.Sprintf("delete stale destination %q", stale[idx].Prefix),
		); err != nil {
			return m.rollbackOperations(rollbackOperations, err)
		}
		if err := m.backend.RouteDel(&deleteRoute); err != nil {
			return m.rollbackOperations(rollbackOperations, fmt.Errorf(
				"reconcile static routes: delete stale destination %q: %w",
				stale[idx].Prefix,
				err,
			))
		}
		rollbackOperations = append(rollbackOperations, rollbackOperation{Route: rollback})
		if err := checkContext(
			ctx,
			fmt.Sprintf("finish stale destination %q", stale[idx].Prefix),
		); err != nil {
			return m.rollbackOperations(rollbackOperations, err)
		}
	}
	return nil
}

type desiredRoute struct {
	Prefix     netip.Prefix
	Interfaces []string
	Route      vnetlink.Route
}

func buildDesiredRoutes(
	routes []Route,
	linkIndexes map[string]int,
	table int,
	protocol vnetlink.RouteProtocol,
	priority int,
) []desiredRoute {
	grouped := map[netip.Prefix][]Route{}
	for _, route := range routes {
		grouped[route.Prefix] = append(grouped[route.Prefix], route)
	}

	prefixes := make([]netip.Prefix, 0, len(grouped))
	for prefix := range grouped {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(first, second int) bool {
		return comparePrefixes(prefixes[first], prefixes[second]) < 0
	})

	desired := make([]desiredRoute, 0, len(prefixes))
	for _, prefix := range prefixes {
		nexthops := append([]Route(nil), grouped[prefix]...)
		sort.Slice(nexthops, func(first, second int) bool {
			comparison := nexthops[first].Nexthop.Compare(nexthops[second].Nexthop)
			if comparison != 0 {
				return comparison < 0
			}
			return nexthops[first].Interface < nexthops[second].Interface
		})

		multipath := make([]*vnetlink.NexthopInfo, 0, len(nexthops))
		interfaceSet := map[string]struct{}{}
		for _, nexthop := range nexthops {
			multipath = append(multipath, &vnetlink.NexthopInfo{
				LinkIndex: linkIndexes[nexthop.Interface],
				Gw:        net.IP(nexthop.Nexthop.AsSlice()),
			})
			interfaceSet[nexthop.Interface] = struct{}{}
		}
		interfaces := make([]string, 0, len(interfaceSet))
		for name := range interfaceSet {
			interfaces = append(interfaces, name)
		}
		sort.Strings(interfaces)

		family := vnetlink.FAMILY_V6
		addressBits := 128
		if prefix.Addr().Is4() {
			family = vnetlink.FAMILY_V4
			addressBits = 32
		}
		desired = append(desired, desiredRoute{
			Prefix:     prefix,
			Interfaces: interfaces,
			Route: vnetlink.Route{
				Scope: vnetlink.SCOPE_UNIVERSE,
				Dst: &net.IPNet{
					IP:   net.IP(prefix.Addr().AsSlice()),
					Mask: net.CIDRMask(prefix.Bits(), addressBits),
				},
				MultiPath: multipath,
				Protocol:  protocol,
				Priority:  priority,
				Family:    family,
				Table:     table,
				Type:      unix.RTN_UNICAST,
			},
		})
	}
	return desired
}

func (m *Reconciler) revalidateInterfaces(
	ctx context.Context,
	interfaceNames []string,
	expectedLinks map[string]vnetlink.Link,
) error {
	for _, name := range interfaceNames {
		if err := checkContext(ctx, fmt.Sprintf("revalidate interface %q", name)); err != nil {
			return err
		}
		expected := expectedLinks[name]
		current, err := m.backend.LinkByName(name)
		if err != nil {
			return fmt.Errorf("resolve interface %q: %w", name, err)
		}
		if err := validateLinkIdentity(expected, current); err != nil {
			return fmt.Errorf("interface %q: %w", name, err)
		}
		if err := checkContext(ctx, fmt.Sprintf("revalidate interface %q", name)); err != nil {
			return err
		}
	}
	return nil
}

func validateLinkIdentity(expected, current vnetlink.Link) error {
	if expected == nil || expected.Attrs() == nil || current == nil || current.Attrs() == nil {
		return errors.New("link is incomplete")
	}
	expectedAttributes := expected.Attrs()
	currentAttributes := current.Attrs()
	if expectedAttributes.Name != currentAttributes.Name ||
		expectedAttributes.Index != currentAttributes.Index ||
		expected.Type() != current.Type() ||
		expectedAttributes.Alias != currentAttributes.Alias ||
		expectedAttributes.ParentIndex != currentAttributes.ParentIndex ||
		!bytes.Equal(expectedAttributes.HardwareAddr, currentAttributes.HardwareAddr) {
		return errors.New("link identity changed during reconciliation")
	}
	expectedVLAN, expectedIsVLAN := expected.(*vnetlink.Vlan)
	currentVLAN, currentIsVLAN := current.(*vnetlink.Vlan)
	if expectedIsVLAN != currentIsVLAN ||
		(expectedIsVLAN && (expectedVLAN.VlanId != currentVLAN.VlanId ||
			expectedVLAN.VlanProtocol != currentVLAN.VlanProtocol)) {
		return errors.New("VLAN identity changed during reconciliation")
	}
	return nil
}

type rollbackRoute struct {
	Route vnetlink.Route
	Links []vnetlink.Link
}

type rollbackOperation struct {
	Route  rollbackRoute
	Remove bool
}

func rollbackRouteForDesired(
	desired desiredRoute,
	expectedLinks map[string]vnetlink.Link,
) rollbackRoute {
	links := make([]vnetlink.Link, 0, len(desired.Interfaces))
	for _, name := range desired.Interfaces {
		links = append(links, expectedLinks[name])
	}
	return rollbackRoute{Route: desired.Route, Links: links}
}

func (m *Reconciler) captureRollbackRoute(kernelRoute vnetlink.Route) (rollbackRoute, error) {
	result := rollbackRoute{Route: kernelRoute}
	indexes := []int{}
	if len(kernelRoute.MultiPath) > 0 {
		for idx, nexthop := range kernelRoute.MultiPath {
			if nexthop == nil {
				return rollbackRoute{}, fmt.Errorf("multipath nexthop %d is nil", idx)
			}
			if nexthop.LinkIndex > 0 {
				indexes = append(indexes, nexthop.LinkIndex)
			}
		}
	} else if kernelRoute.LinkIndex > 0 {
		indexes = append(indexes, kernelRoute.LinkIndex)
	}
	sort.Ints(indexes)
	seen := map[int]struct{}{}
	for _, index := range indexes {
		if _, duplicate := seen[index]; duplicate {
			continue
		}
		seen[index] = struct{}{}
		link, err := m.backend.LinkByIndex(index)
		if err != nil {
			return rollbackRoute{}, fmt.Errorf("resolve link index %d: %w", index, err)
		}
		if link == nil || link.Attrs() == nil || link.Attrs().Index != index {
			return rollbackRoute{}, fmt.Errorf("resolve link index %d: incomplete or mismatched link", index)
		}
		result.Links = append(result.Links, link)
	}
	return result, nil
}

func (m *Reconciler) rollbackOperations(
	operations []rollbackOperation,
	cause error,
) error {
	rollbackErrors := []error{cause}
	for _, operation := range slices.Backward(operations) {
		if err := m.revalidateLinks(operation.Route.Links); err != nil {
			action := "restore deleted route"
			if operation.Remove {
				action = "remove added route"
			}
			rollbackErrors = append(rollbackErrors, fmt.Errorf(
				"reconcile static routes: cannot safely %s: %w",
				action,
				err,
			))
			continue
		}

		rollbackRoute := operation.Route.Route
		var err error
		action := "restore deleted route"
		if operation.Remove {
			action = "remove added route"
			err = m.backend.RouteDel(&rollbackRoute)
		} else {
			err = m.backend.RouteAdd(&rollbackRoute)
		}
		if err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf(
				"reconcile static routes: %s: %w",
				action,
				err,
			))
		}
	}
	return errors.Join(rollbackErrors...)
}

func (m *Reconciler) revalidateLinks(expectedLinks []vnetlink.Link) error {
	for _, expected := range expectedLinks {
		if expected == nil || expected.Attrs() == nil {
			return errors.New("captured link is incomplete")
		}
		current, err := m.backend.LinkByName(expected.Attrs().Name)
		if err != nil {
			return fmt.Errorf("resolve interface %q: %w", expected.Attrs().Name, err)
		}
		if err := validateLinkIdentity(expected, current); err != nil {
			return fmt.Errorf("interface %q: %w", expected.Attrs().Name, err)
		}
	}
	return nil
}

func (m *Reconciler) owns(route vnetlink.Route) bool {
	return route.Table == m.table &&
		route.Protocol == m.protocol &&
		route.Priority == m.priority
}

func routesEquivalent(current, desired vnetlink.Route) bool {
	if current.Table != desired.Table ||
		current.Protocol != desired.Protocol ||
		current.Priority != desired.Priority ||
		current.Family != desired.Family ||
		current.Scope != desired.Scope ||
		current.Tos != desired.Tos ||
		current.Type != desired.Type ||
		!routeAttributesEquivalent(current, desired) {
		return false
	}
	currentPrefix, currentIsIP, currentErr := prefixFromKernelRoute(current)
	desiredPrefix, desiredIsIP, desiredErr := prefixFromKernelRoute(desired)
	if currentErr != nil || desiredErr != nil || !currentIsIP || !desiredIsIP || currentPrefix != desiredPrefix {
		return false
	}

	currentNexthops := normalizedNexthops(current)
	desiredNexthops := normalizedNexthops(desired)
	if len(currentNexthops) != len(desiredNexthops) {
		return false
	}
	for idx := range currentNexthops {
		if !nexthopsEquivalent(currentNexthops[idx], desiredNexthops[idx]) {
			return false
		}
	}
	return true
}

func routeAttributesEquivalent(current, desired vnetlink.Route) bool {
	if (len(current.MultiPath) != 0 && (current.LinkIndex != 0 || current.Gw != nil)) ||
		(len(desired.MultiPath) != 0 && (desired.LinkIndex != 0 || desired.Gw != nil)) {
		return false
	}
	for _, nexthop := range current.MultiPath {
		if nexthop == nil {
			return false
		}
	}
	for _, nexthop := range desired.MultiPath {
		if nexthop == nil {
			return false
		}
	}
	return current.ILinkIndex == desired.ILinkIndex &&
		bytes.Equal(current.Src, desired.Src) &&
		current.Flags&^kernelManagedRouteFlags == desired.Flags&^kernelManagedRouteFlags &&
		current.Realm == desired.Realm &&
		current.MTU == desired.MTU &&
		current.MTULock == desired.MTULock &&
		current.Window == desired.Window &&
		current.Rtt == desired.Rtt &&
		current.RttVar == desired.RttVar &&
		current.Ssthresh == desired.Ssthresh &&
		current.Cwnd == desired.Cwnd &&
		current.AdvMSS == desired.AdvMSS &&
		current.Reordering == desired.Reordering &&
		current.Hoplimit == desired.Hoplimit &&
		current.InitCwnd == desired.InitCwnd &&
		current.Features == desired.Features &&
		current.RtoMin == desired.RtoMin &&
		current.RtoMinLock == desired.RtoMinLock &&
		current.InitRwnd == desired.InitRwnd &&
		current.QuickACK == desired.QuickACK &&
		current.Congctl == desired.Congctl &&
		current.FastOpenNoCookie == desired.FastOpenNoCookie &&
		current.NewDst == nil && desired.NewDst == nil &&
		current.Encap == nil && desired.Encap == nil &&
		current.Via == nil && desired.Via == nil
}

func nexthopsEquivalent(current, desired vnetlink.NexthopInfo) bool {
	return current.LinkIndex == desired.LinkIndex &&
		current.Hops == desired.Hops &&
		current.Gw.Equal(desired.Gw) &&
		current.Flags&^kernelManagedNexthopFlags == desired.Flags&^kernelManagedNexthopFlags &&
		current.NewDst == nil && desired.NewDst == nil &&
		current.Encap == nil && desired.Encap == nil &&
		current.Via == nil && desired.Via == nil
}

func routeForDelete(kernelRoute vnetlink.Route, prefix netip.Prefix) vnetlink.Route {
	if kernelRoute.Dst != nil {
		return kernelRoute
	}
	addressBits := 128
	if prefix.Addr().Is4() {
		addressBits = 32
	}
	kernelRoute.Dst = &net.IPNet{
		IP:   net.IP(prefix.Addr().AsSlice()),
		Mask: net.CIDRMask(prefix.Bits(), addressBits),
	}
	return kernelRoute
}

func normalizedNexthops(kernelRoute vnetlink.Route) []vnetlink.NexthopInfo {
	nexthops := make([]vnetlink.NexthopInfo, 0, max(1, len(kernelRoute.MultiPath)))
	if len(kernelRoute.MultiPath) > 0 {
		for _, nexthop := range kernelRoute.MultiPath {
			if nexthop != nil {
				nexthops = append(nexthops, *nexthop)
			}
		}
	} else if kernelRoute.LinkIndex != 0 || kernelRoute.Gw != nil {
		nexthops = append(nexthops, vnetlink.NexthopInfo{
			LinkIndex: kernelRoute.LinkIndex,
			Gw:        kernelRoute.Gw,
		})
	}
	sort.Slice(nexthops, func(first, second int) bool {
		if comparison := bytes.Compare(nexthops[first].Gw, nexthops[second].Gw); comparison != 0 {
			return comparison < 0
		}
		return nexthops[first].LinkIndex < nexthops[second].LinkIndex
	})
	return nexthops
}

func prefixFromKernelRoute(route vnetlink.Route) (netip.Prefix, bool, error) {
	if route.MPLSDst != nil {
		return netip.Prefix{}, false, nil
	}
	if route.Dst == nil {
		switch route.Family {
		case vnetlink.FAMILY_V4:
			return netip.PrefixFrom(netip.IPv4Unspecified(), 0), true, nil
		case vnetlink.FAMILY_V6:
			return netip.PrefixFrom(netip.IPv6Unspecified(), 0), true, nil
		default:
			return netip.Prefix{}, false, nil
		}
	}

	ones, bits := route.Dst.Mask.Size()
	if bits != 32 && bits != 128 {
		return netip.Prefix{}, false, fmt.Errorf(
			"destination %q has a non-CIDR mask",
			route.Dst,
		)
	}
	var (
		address netip.Addr
		family  int
	)
	if bits == 32 {
		raw := route.Dst.IP.To4()
		if raw == nil {
			return netip.Prefix{}, false, fmt.Errorf(
				"destination %q has an invalid IPv4 address",
				route.Dst,
			)
		}
		address = netip.AddrFrom4([4]byte(raw))
		family = vnetlink.FAMILY_V4
	} else {
		raw := route.Dst.IP.To16()
		if raw == nil {
			return netip.Prefix{}, false, fmt.Errorf(
				"destination %q has an invalid IPv6 address",
				route.Dst,
			)
		}
		address = netip.AddrFrom16([16]byte(raw))
		family = vnetlink.FAMILY_V6
	}
	if route.Family != 0 && route.Family != family {
		return netip.Prefix{}, false, fmt.Errorf(
			"destination %q family %d does not match mask family %d",
			route.Dst,
			route.Family,
			family,
		)
	}
	return netip.PrefixFrom(address, ones).Masked(), true, nil
}

func comparePrefixes(first, second netip.Prefix) int {
	if comparison := first.Addr().Compare(second.Addr()); comparison != 0 {
		return comparison
	}
	switch {
	case first.Bits() < second.Bits():
		return -1
	case first.Bits() > second.Bits():
		return 1
	default:
		return 0
	}
}

func checkContext(ctx context.Context, operation string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("reconcile static routes: %s: %w", operation, err)
	}
	return nil
}

var _ Backend = (*vnetlink.Handle)(nil)
