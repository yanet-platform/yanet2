package neighbour

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

const (
	ownedTablePrefix      = "netlink-dataplane-"
	gatewayAttemptTimeout = 5 * time.Second
)

// ValidateTableName checks that a neighbour table is in the namespace owned by
// the netlink dataplane sidecar.
func ValidateTableName(tableName string) error {
	if tableName == "" {
		return errors.New("neighbour table name is empty")
	}
	if !strings.HasPrefix(tableName, ownedTablePrefix) {
		return fmt.Errorf(
			"neighbour table %q is outside reserved %q namespace",
			tableName,
			ownedTablePrefix,
		)
	}
	return nil
}

// Client is the route operator surface needed to publish neighbours.
type Client interface {
	ListTables(
		context.Context,
		*operatorpb.ListNeighbourTablesRequest,
		...grpc.CallOption,
	) (*operatorpb.ListNeighbourTablesResponse, error)
	CreateTable(
		context.Context,
		*operatorpb.CreateNeighbourTableRequest,
		...grpc.CallOption,
	) (*operatorpb.CreateNeighbourTableResponse, error)
	UpdateTable(
		context.Context,
		*operatorpb.UpdateNeighbourTableRequest,
		...grpc.CallOption,
	) (*operatorpb.UpdateNeighbourTableResponse, error)
	RemoveTable(
		context.Context,
		*operatorpb.RemoveNeighbourTableRequest,
		...grpc.CallOption,
	) (*operatorpb.RemoveNeighbourTableResponse, error)
	List(
		context.Context,
		*operatorpb.ListNeighboursRequest,
		...grpc.CallOption,
	) (*operatorpb.ListNeighboursResponse, error)
	UpdateNeighbours(
		context.Context,
		*operatorpb.UpdateNeighboursRequest,
		...grpc.CallOption,
	) (*operatorpb.UpdateNeighboursResponse, error)
	RemoveNeighbours(
		context.Context,
		*operatorpb.RemoveNeighboursRequest,
		...grpc.CallOption,
	) (*operatorpb.RemoveNeighboursResponse, error)
}

// GatewayTarget describes one independently managed neighbour table.
//
// TableName must start with "netlink-dataplane-". Configuring a table in this
// namespace declares the table sidecar-owned: reconciliation may create it,
// change its priority, remove absent entries, and delete obsolete prefixed
// tables that are not configured by any target. An empty device list owns all
// logical devices in the snapshot.
type GatewayTarget struct {
	Name            string
	TableName       string
	DefaultPriority uint32
	Devices         []string
	Client          Client
}

// ValidateManagedDeviceOwnership verifies that every managed link resolves to
// one distinct logical device owned by exactly one configured gateway.
func ValidateManagedDeviceOwnership(
	state netplan.State,
	linkMap map[string]string,
	targets []GatewayTarget,
) error {
	_, devicesByLink, err := managedLinkConfiguration(state, linkMap)
	if err != nil {
		return fmt.Errorf("validate managed devices: %w", err)
	}
	if err := validateOwnership(nil, targets); err != nil {
		return err
	}
	if len(targets) == 1 && len(targets[0].Devices) == 0 {
		return nil
	}

	deviceOwners := make(map[string]struct{}, len(devicesByLink))
	for _, target := range targets {
		for _, device := range target.Devices {
			deviceOwners[device] = struct{}{}
		}
	}
	linkNames := make([]string, 0, len(devicesByLink))
	for linkName := range devicesByLink {
		linkNames = append(linkNames, linkName)
	}
	sort.Strings(linkNames)
	for _, linkName := range linkNames {
		device := devicesByLink[linkName]
		if _, owned := deviceOwners[device]; !owned {
			return fmt.Errorf(
				"managed link %q logical device %q has no gateway owner",
				linkName,
				device,
			)
		}
	}
	return nil
}

// Publish reconciles a desired snapshot across every gateway target.
//
// All gateways must reach the same route operator. Each target attempt has an
// independent deadline; a failure does not prevent later targets from being
// attempted unless the caller cancels. Failures are joined, and obsolete tables
// are removed only after every configured replacement succeeds.
func Publish(ctx context.Context, entries []Entry, targets []GatewayTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateOwnership(entries, targets); err != nil {
		return err
	}
	protectedTables := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		protectedTables[target.TableName] = struct{}{}
	}

	var tables []*operatorpb.NeighbourTableInfo
	var joinedError error
	for idx, target := range targets {
		if err := ctx.Err(); err != nil {
			return errors.Join(joinedError, err)
		}
		targetTables, err := publishTarget(ctx, entries, target)
		if err != nil {
			name := target.Name
			if name == "" {
				name = fmt.Sprintf("target[%d]", idx)
			}
			joinedError = errors.Join(
				joinedError,
				fmt.Errorf("publish neighbours to gateway %q: %w", name, err),
			)
			continue
		}
		tables = targetTables
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(joinedError, err)
	}
	if joinedError != nil {
		return joinedError
	}
	if err := removeStaleTables(ctx, targets[len(targets)-1], tables, protectedTables); err != nil {
		return fmt.Errorf("clean up shared neighbour tables: %w", err)
	}
	return nil
}

func validateOwnership(entries []Entry, targets []GatewayTarget) error {
	if len(targets) == 0 {
		return errors.New("at least one gateway target is required")
	}
	deviceOwners := map[string]string{}
	for idx, target := range targets {
		if err := ValidateTableName(target.TableName); err != nil {
			return fmt.Errorf("validate target %d: %w", idx, err)
		}
		if target.Client == nil {
			return fmt.Errorf("validate target %d: route neighbour client is nil", idx)
		}
		if len(targets) > 1 && len(target.Devices) == 0 {
			return fmt.Errorf("validate target %d: devices are required with multiple targets", idx)
		}
		for _, device := range target.Devices {
			if device == "" {
				return fmt.Errorf("validate target %d: device name is empty", idx)
			}
			name := target.Name
			if name == "" {
				name = fmt.Sprintf("target[%d]", idx)
			}
			if previous, duplicate := deviceOwners[device]; duplicate {
				return fmt.Errorf(
					"validate target %d: device %q is already owned by %q",
					idx,
					device,
					previous,
				)
			}
			deviceOwners[device] = name
		}
	}

	if len(targets) == 1 && len(targets[0].Devices) == 0 {
		return nil
	}
	for idx, entry := range entries {
		if _, owned := deviceOwners[entry.HardwareRoute.Device]; !owned {
			return fmt.Errorf(
				"desired neighbour %d device %q has no gateway owner",
				idx,
				entry.HardwareRoute.Device,
			)
		}
	}
	return nil
}

func publishTarget(
	ctx context.Context,
	entries []Entry,
	target GatewayTarget,
) ([]*operatorpb.NeighbourTableInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, gatewayAttemptTimeout)
	defer cancel()

	desired, err := filterEntries(entries, target.Devices)
	if err != nil {
		return nil, err
	}
	tables, err := ensureTable(ctx, target)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	response, err := target.Client.List(
		ctx,
		&operatorpb.ListNeighboursRequest{Table: target.TableName},
	)
	if err != nil {
		return nil, fmt.Errorf("list table %q: %w", target.TableName, err)
	}
	if response == nil {
		return nil, fmt.Errorf("list table %q: incomplete response", target.TableName)
	}
	current, err := parseCurrentEntries(response.GetNeighbours())
	if err != nil {
		return nil, fmt.Errorf("list table %q: %w", target.TableName, err)
	}

	upserts := make([]Entry, 0, len(desired))
	desiredNextHops := map[netip.Addr]struct{}{}
	for _, entry := range desired {
		desiredNextHops[entry.NextHop] = struct{}{}
		existing, found := current[entry.NextHop]
		if !found || !matchesCurrent(entry, existing, target.DefaultPriority) {
			upserts = append(upserts, entry)
		}
	}

	removals := make([]netip.Addr, 0, len(current))
	for nextHop := range current {
		if _, wanted := desiredNextHops[nextHop]; !wanted {
			removals = append(removals, nextHop)
		}
	}
	sort.Slice(removals, func(first, second int) bool {
		return removals[first].Compare(removals[second]) < 0
	})

	if len(upserts) != 0 {
		wireEntries := make([]*operatorpb.NeighbourEntry, 0, len(upserts))
		for _, entry := range upserts {
			wireEntries = append(wireEntries, entryToProto(entry))
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		updateResponse, err := target.Client.UpdateNeighbours(
			ctx,
			&operatorpb.UpdateNeighboursRequest{
				Table:   target.TableName,
				Entries: wireEntries,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("update table %q: %w", target.TableName, err)
		}
		if updateResponse == nil {
			return nil, fmt.Errorf("update table %q: incomplete response", target.TableName)
		}
	}

	if len(removals) != 0 {
		nextHops := make([]*commonpb.IPAddress, 0, len(removals))
		for _, nextHop := range removals {
			nextHops = append(nextHops, commonpb.NewIPAddressFromAddr(nextHop))
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		removeResponse, err := target.Client.RemoveNeighbours(
			ctx,
			&operatorpb.RemoveNeighboursRequest{
				Table:    target.TableName,
				NextHops: nextHops,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("remove from table %q: %w", target.TableName, err)
		}
		if removeResponse == nil {
			return nil, fmt.Errorf("remove from table %q: incomplete response", target.TableName)
		}
	}
	return tables, ctx.Err()
}

func filterEntries(entries []Entry, devices []string) ([]Entry, error) {
	deviceSet := map[string]struct{}{}
	for _, device := range devices {
		if device != "" {
			deviceSet[device] = struct{}{}
		}
	}

	filtered := make([]Entry, 0, len(entries))
	seen := map[netip.Addr]struct{}{}
	for _, entry := range entries {
		if len(deviceSet) != 0 {
			if _, owned := deviceSet[entry.HardwareRoute.Device]; !owned {
				continue
			}
		}
		if !entry.NextHop.IsValid() || entry.NextHop.Zone() != "" {
			return nil, fmt.Errorf("desired next hop %q is invalid", entry.NextHop)
		}
		if _, duplicate := seen[entry.NextHop]; duplicate {
			return nil, fmt.Errorf("duplicate desired next hop %q", entry.NextHop)
		}
		seen[entry.NextHop] = struct{}{}
		filtered = append(filtered, entry)
	}
	sort.Slice(filtered, func(first, second int) bool {
		return filtered[first].NextHop.Compare(filtered[second].NextHop) < 0
	})
	return filtered, nil
}

func ensureTable(
	ctx context.Context,
	target GatewayTarget,
) ([]*operatorpb.NeighbourTableInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := target.Client.ListTables(
		ctx,
		&operatorpb.ListNeighbourTablesRequest{},
	)
	if err != nil {
		return nil, fmt.Errorf("list neighbour tables: %w", err)
	}
	if response == nil {
		return nil, errors.New("list neighbour tables: incomplete response")
	}
	tables := response.GetTables()

	var existing *operatorpb.NeighbourTableInfo
	for idx, table := range tables {
		if table == nil {
			return nil, fmt.Errorf("list neighbour tables: entry %d is nil", idx)
		}
		if table.GetName() != target.TableName {
			continue
		}
		if existing != nil {
			return nil, fmt.Errorf("list neighbour tables: duplicate table %q", target.TableName)
		}
		existing = table
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if existing == nil {
		created, err := target.Client.CreateTable(
			ctx,
			&operatorpb.CreateNeighbourTableRequest{
				Name:            target.TableName,
				DefaultPriority: target.DefaultPriority,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("create neighbour table %q: %w", target.TableName, err)
		}
		if created == nil {
			return nil, fmt.Errorf(
				"create neighbour table %q: incomplete response",
				target.TableName,
			)
		}
		return tables, nil
	}

	if existing.GetBuiltIn() {
		return nil, fmt.Errorf("neighbour table %q is built in", target.TableName)
	}
	if existing.GetDefaultPriority() == target.DefaultPriority {
		return tables, nil
	}

	updated, err := target.Client.UpdateTable(
		ctx,
		&operatorpb.UpdateNeighbourTableRequest{
			Name:            target.TableName,
			DefaultPriority: target.DefaultPriority,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("update neighbour table %q: %w", target.TableName, err)
	}
	if updated == nil {
		return nil, fmt.Errorf(
			"update neighbour table %q: incomplete response",
			target.TableName,
		)
	}
	return tables, nil
}

func removeStaleTables(
	ctx context.Context,
	target GatewayTarget,
	tables []*operatorpb.NeighbourTableInfo,
	protectedTables map[string]struct{},
) error {
	ctx, cancel := context.WithTimeout(ctx, gatewayAttemptTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	stale := make([]*operatorpb.NeighbourTableInfo, 0, len(tables))
	seen := make(map[string]struct{}, len(tables))
	for _, table := range tables {
		name := table.GetName()
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("list neighbour tables: duplicate table %q", name)
		}
		seen[name] = struct{}{}
		if _, protected := protectedTables[name]; protected {
			continue
		}
		if !strings.HasPrefix(name, ownedTablePrefix) {
			continue
		}
		if table.GetBuiltIn() {
			return fmt.Errorf("obsolete neighbour table %q is built in", name)
		}
		stale = append(stale, table)
	}
	sort.Slice(stale, func(first, second int) bool {
		return stale[first].GetName() < stale[second].GetName()
	})
	for _, table := range stale {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := table.GetName()
		response, err := target.Client.RemoveTable(
			ctx,
			&operatorpb.RemoveNeighbourTableRequest{Name: name},
		)
		if err != nil {
			return fmt.Errorf("remove obsolete neighbour table %q: %w", name, err)
		}
		if response == nil {
			return fmt.Errorf("remove obsolete neighbour table %q: incomplete response", name)
		}
	}
	return ctx.Err()
}

type currentEntry struct {
	HardwareRoute HardwareRoute
	Priority      uint32
}

func parseCurrentEntries(
	entries []*operatorpb.NeighbourEntry,
) (map[netip.Addr]currentEntry, error) {
	current := map[netip.Addr]currentEntry{}
	for idx, entry := range entries {
		if entry == nil {
			return nil, fmt.Errorf("neighbour entry %d is nil", idx)
		}
		if entry.GetNextHop() == nil {
			return nil, fmt.Errorf("neighbour entry %d has no next hop", idx)
		}
		nextHop, err := entry.GetNextHop().ToAddr()
		if err != nil {
			return nil, fmt.Errorf("neighbour entry %d has invalid next hop: %w", idx, err)
		}
		if _, duplicate := current[nextHop]; duplicate {
			return nil, fmt.Errorf("duplicate current next hop %q", nextHop)
		}

		sourceMAC, err := macFromProto(entry.GetHardwareAddr())
		if err != nil {
			return nil, fmt.Errorf("neighbour entry %q has invalid source MAC: %w", nextHop, err)
		}
		destinationMAC, err := macFromProto(entry.GetLinkAddr())
		if err != nil {
			return nil, fmt.Errorf(
				"neighbour entry %q has invalid destination MAC: %w",
				nextHop,
				err,
			)
		}
		current[nextHop] = currentEntry{
			HardwareRoute: HardwareRoute{
				SourceMAC:      sourceMAC,
				DestinationMAC: destinationMAC,
				Device:         entry.GetDevice(),
			},
			Priority: entry.GetPriority(),
		}
	}
	return current, nil
}

func macFromProto(address *commonpb.MACAddress) ([6]byte, error) {
	if address == nil {
		return [6]byte{}, errors.New("address is missing")
	}
	if address.GetAddr()>>48 != 0 {
		return [6]byte{}, errors.New("upper 16 bits are set")
	}
	return address.EUI48(), nil
}

func matchesCurrent(entry Entry, current currentEntry, defaultPriority uint32) bool {
	// The server normalizes updates to permanent state and owns timestamps and
	// source metadata, so only forwarding identity and effective priority diff.
	return entry.HardwareRoute == current.HardwareRoute && current.Priority == defaultPriority
}

func entryToProto(entry Entry) *operatorpb.NeighbourEntry {
	return &operatorpb.NeighbourEntry{
		NextHop:      commonpb.NewIPAddressFromAddr(entry.NextHop),
		LinkAddr:     commonpb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
		HardwareAddr: commonpb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
		State:        operatorpb.NeighbourState(entry.State),
		Device:       entry.HardwareRoute.Device,
	}
}
