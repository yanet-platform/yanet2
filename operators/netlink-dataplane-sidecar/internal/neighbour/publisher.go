package neighbour

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

const ownedTablePrefix = "netlink-dataplane-"

// ValidateTableName checks the bounded namespace reserved for sidecar input.
func ValidateTableName(tableName string) error {
	if !strings.HasPrefix(tableName, ownedTablePrefix) {
		return fmt.Errorf("neighbour table %q is outside reserved %q namespace", tableName, ownedTablePrefix)
	}
	if len(tableName) <= len(ownedTablePrefix) || len(tableName) > operatorpb.NeighbourTableNameBytes {
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

// Client can replace a snapshot but cannot enumerate or delete other tables.
type Client interface {
	ReplaceNeighbours(context.Context, *operatorpb.ReplaceNeighboursRequest, ...grpc.CallOption) (*operatorpb.ReplaceNeighboursResponse, error)
}

// GatewayTarget is an alternate transport to the same route operator.
type GatewayTarget struct {
	Name   string
	Client Client
}

// PublicationConfig describes the single table owned by a sidecar namespace.
type PublicationConfig struct {
	TableName       string
	DefaultPriority uint32
	Timeout         time.Duration
}

// Validate requires a stable source identity and bounded transport attempts.
func (m PublicationConfig) Validate() error {
	if err := ValidateTableName(m.TableName); err != nil {
		return err
	}
	if m.DefaultPriority == 0 {
		return errors.New("neighbour_priority must be positive")
	}
	if m.Timeout <= 0 {
		return errors.New("neighbour_publish_timeout must be positive")
	}
	return nil
}

// ValidateManagedDevices checks the complete OS-to-logical egress mapping.
func ValidateManagedDevices(state desired.State, linkMap map[string]string) error {
	_, _, err := managedLinkConfiguration(state, linkMap)
	return err
}

// Publish tries complete replacements sequentially until one transport succeeds.
//
// One namespace has one producer. The operator serializes successive calls;
// retrying after an unknown response is safe because replacement is idempotent.
func Publish(ctx context.Context, entries []Entry, targets []GatewayTarget, config PublicationConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("at least one gateway target is required")
	}
	for _, target := range targets {
		if target.Client == nil {
			return errors.New("route neighbour client is nil")
		}
	}
	request, err := prepareRequest(ctx, entries, config)
	if err != nil {
		return err
	}
	var failures error
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		attempt, cancel := context.WithTimeout(ctx, config.Timeout)
		response, err := target.Client.ReplaceNeighbours(attempt, request)
		cancel()
		if err == nil && response == nil {
			err = errors.New("replace neighbours: incomplete response")
		}
		if err == nil {
			return nil
		}
		failures = errors.Join(failures, fmt.Errorf("gateway %q: %w", target.Name, err))
	}
	return failures
}

func prepareRequest(ctx context.Context, entries []Entry, config PublicationConfig) (*operatorpb.ReplaceNeighboursRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(entries) > operatorpb.NeighbourSnapshotEntries {
		return nil, errors.New("neighbour snapshot exceeds entry limit")
	}
	desired := slices.Clone(entries)
	for idx := range desired {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := &desired[idx]
		if !entry.NextHop.IsValid() || entry.NextHop.Zone() != "" {
			return nil, fmt.Errorf("desired next hop %q is invalid", entry.NextHop)
		}
		entry.NextHop = entry.NextHop.Unmap()
		if err := operatorpb.ValidateNeighbourDevice(entry.HardwareRoute.Device); err != nil {
			return nil, err
		}
		if entry.HardwareRoute.SourceMAC == [6]byte{} || entry.HardwareRoute.DestinationMAC == [6]byte{} {
			return nil, fmt.Errorf("desired next hop %s has an unusable hardware address", entry.NextHop)
		}
	}
	slices.SortFunc(desired, func(left, right Entry) int {
		return left.NextHop.Compare(right.NextHop)
	})
	for idx := 1; idx < len(desired); idx++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		left, right := desired[idx-1], desired[idx]
		if left.NextHop == right.NextHop {
			return nil, fmt.Errorf("duplicate desired next hop %s", right.NextHop)
		}
	}
	request := &operatorpb.ReplaceNeighboursRequest{
		Table: config.TableName, DefaultPriority: config.DefaultPriority,
		Entries: make([]*operatorpb.NeighbourEntry, 0, len(desired)),
	}
	for _, entry := range desired {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		request.Entries = append(request.Entries, &operatorpb.NeighbourEntry{
			NextHop:      commonpb.NewIPAddressFromAddr(entry.NextHop),
			LinkAddr:     commonpb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
			HardwareAddr: commonpb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
			State:        operatorpb.NeighbourState_NUD_PERMANENT,
			Device:       entry.HardwareRoute.Device,
		})
	}
	if proto.Size(request) > operatorpb.NeighbourSnapshotBytes {
		return nil, errors.New("neighbour snapshot exceeds byte limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return request, nil
}
