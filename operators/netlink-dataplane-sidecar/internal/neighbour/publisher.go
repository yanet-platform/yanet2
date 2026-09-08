package neighbour

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	publicationChunkSize  = 1000
)

// ValidateTableName checks the bounded namespace owned by the sidecar.
func ValidateTableName(tableName string) error {
	if !strings.HasPrefix(tableName, ownedTablePrefix) {
		return fmt.Errorf("neighbour table %q is outside reserved %q namespace", tableName, ownedTablePrefix)
	}
	if len(tableName) <= len(ownedTablePrefix) || len(tableName) > 128 {
		return errors.New("neighbour table name requires a suffix and at most 128 bytes")
	}
	for _, character := range tableName {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return errors.New("neighbour table name contains an invalid character")
	}
	return nil
}

// Client is the route operator surface needed to publish neighbours.
type Client interface {
	ReplaceNeighbours(
		context.Context,
		...grpc.CallOption,
	) (grpc.ClientStreamingClient[operatorpb.ReplaceNeighboursRequest, operatorpb.ReplaceNeighboursResponse], error)
	ListTables(
		context.Context,
		*operatorpb.ListNeighbourTablesRequest,
		...grpc.CallOption,
	) (*operatorpb.ListNeighbourTablesResponse, error)
	RemoveTable(
		context.Context,
		*operatorpb.RemoveNeighbourTableRequest,
		...grpc.CallOption,
	) (*operatorpb.RemoveNeighbourTableResponse, error)
}

// GatewayTarget describes one independently managed neighbour table.
//
// The sidecar owns prefixed tables: it replaces their entries and priority,
// then deletes obsolete prefixed tables not configured by any target. An empty
// device list owns all logical devices in the snapshot.
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
	deviceOwners := map[string]struct{}{}
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
			return fmt.Errorf("managed link %q logical device %q has no gateway owner", linkName, device)
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
	protectedTables := map[string]struct{}{}
	for _, target := range targets {
		protectedTables[target.TableName] = struct{}{}
	}
	var joinedError error
	for idx, target := range targets {
		if err := ctx.Err(); err != nil {
			return errors.Join(joinedError, err)
		}
		if err := publishTarget(ctx, entries, target); err != nil {
			name := target.Name
			if name == "" {
				name = fmt.Sprintf("target[%d]", idx)
			}
			joinedError = errors.Join(joinedError, fmt.Errorf("publish neighbours to gateway %q: %w", name, err))
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(joinedError, err)
	}
	if joinedError != nil {
		return joinedError
	}
	if err := removeStaleTables(ctx, targets[len(targets)-1], protectedTables); err != nil {
		return fmt.Errorf("clean up shared neighbour tables: %w", err)
	}
	return nil
}

func validateOwnership(entries []Entry, targets []GatewayTarget) error {
	if len(targets) == 0 {
		return errors.New("at least one gateway target is required")
	}
	deviceOwners := map[string]string{}
	tables := map[string]struct{}{}
	for idx, target := range targets {
		if err := ValidateTableName(target.TableName); err != nil {
			return fmt.Errorf("validate target %d: %w", idx, err)
		}
		if _, duplicate := tables[target.TableName]; duplicate {
			return fmt.Errorf("validate target %d: duplicate table %q", idx, target.TableName)
		}
		tables[target.TableName] = struct{}{}
		if target.Client == nil {
			return fmt.Errorf("validate target %d: route neighbour client is nil", idx)
		}
		if len(targets) > 1 && len(target.Devices) == 0 {
			return fmt.Errorf("validate target %d: devices are required with multiple targets", idx)
		}
		for _, device := range target.Devices {
			if device == "" || len(device) > 128 {
				return fmt.Errorf("validate target %d: device name must contain 1..128 bytes", idx)
			}
			name := target.Name
			if name == "" {
				name = fmt.Sprintf("target[%d]", idx)
			}
			if previous, duplicate := deviceOwners[device]; duplicate {
				return fmt.Errorf("validate target %d: device %q is already owned by %q", idx, device, previous)
			}
			deviceOwners[device] = name
		}
	}
	if len(targets) == 1 && len(targets[0].Devices) == 0 {
		return nil
	}
	for idx, entry := range entries {
		if _, owned := deviceOwners[entry.HardwareRoute.Device]; !owned {
			return fmt.Errorf("desired neighbour %d device %q has no gateway owner", idx, entry.HardwareRoute.Device)
		}
	}
	return nil
}

func publishTarget(ctx context.Context, entries []Entry, target GatewayTarget) error {
	ctx, cancel := context.WithTimeout(ctx, gatewayAttemptTimeout)
	defer cancel()

	desired, err := filterEntries(entries, target.Devices)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stream, err := target.Client.ReplaceNeighbours(ctx)
	if err != nil {
		return err
	}
	if stream == nil {
		return errors.New("replace neighbours: incomplete stream")
	}
	// Fixed-width addresses and bounded names keep these chunks below 256 KiB.
	// An empty snapshot still sends metadata before the clean half-close.
	for start := 0; ; {
		end := min(start+publicationChunkSize, len(desired))
		request := &operatorpb.ReplaceNeighboursRequest{
			Table: target.TableName, DefaultPriority: target.DefaultPriority,
			Entries: make([]*operatorpb.NeighbourEntry, 0, end-start),
		}
		for _, entry := range desired[start:end] {
			request.Entries = append(request.Entries, entryToProto(entry))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := stream.Send(request); err != nil {
			if errors.Is(err, io.EOF) {
				_, resultError := stream.CloseAndRecv()
				if resultError != nil {
					return resultError
				}
				return fmt.Errorf("replace neighbours: stream closed before snapshot completed: %w", err)
			}
			return err
		}
		if end == len(desired) {
			break
		}
		start = end
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		return err
	}
	if response == nil {
		return errors.New("replace neighbours: incomplete response")
	}
	return ctx.Err()
}

func filterEntries(entries []Entry, devices []string) ([]Entry, error) {
	deviceSet := map[string]struct{}{}
	for _, device := range devices {
		deviceSet[device] = struct{}{}
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
		if len(entry.HardwareRoute.Device) > 128 {
			return nil, errors.New("desired device exceeds 128 bytes")
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

func removeStaleTables(ctx context.Context, target GatewayTarget, protectedTables map[string]struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, gatewayAttemptTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	response, err := target.Client.ListTables(ctx, &operatorpb.ListNeighbourTablesRequest{})
	if err != nil {
		return fmt.Errorf("list neighbour tables: %w", err)
	}
	if response == nil {
		return errors.New("list neighbour tables: incomplete response")
	}
	stale := []string{}
	seen := map[string]struct{}{}
	for _, table := range response.GetTables() {
		if table == nil {
			return errors.New("list neighbour tables: nil table")
		}
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
		stale = append(stale, name)
	}
	sort.Strings(stale)
	for _, name := range stale {
		if err := ctx.Err(); err != nil {
			return err
		}
		response, err := target.Client.RemoveTable(ctx, &operatorpb.RemoveNeighbourTableRequest{Name: name})
		if err != nil {
			return fmt.Errorf("remove obsolete neighbour table %q: %w", name, err)
		}
		if response == nil {
			return fmt.Errorf("remove obsolete neighbour table %q: incomplete response", name)
		}
	}
	return ctx.Err()
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
