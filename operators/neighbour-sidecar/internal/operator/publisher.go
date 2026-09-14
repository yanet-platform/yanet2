package operator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

type publisherTarget struct {
	Name       string
	Connection *grpc.ClientConn
	Client     operatorpb.NeighbourServiceClient
}

// Publisher replaces one table through alternate transports to the same receiver.
type Publisher struct {
	targets         []publisherTarget
	tableName       string
	defaultPriority uint32
	timeout         time.Duration
}

// NewPublisher opens lazy connections using the common gateway TLS settings.
func NewPublisher(cfg *Config) (*Publisher, error) {
	m := &Publisher{
		tableName:       cfg.TableName,
		defaultPriority: cfg.DefaultPriority,
		timeout:         cfg.PublishTimeout,
	}
	for _, gateway := range cfg.Gateways {
		connection, err := operator.DialGateway(gateway)
		if err != nil {
			return nil, errors.Join(err, m.Close())
		}
		m.targets = append(m.targets, publisherTarget{
			Name: gateway.Name, Connection: connection,
			Client: operatorpb.NewNeighbourServiceClient(connection),
		})
	}
	return m, nil
}

// Apply sends the complete observation and creates its source when absent.
//
// The reconciler serializes calls and retries failed publications. Repeating
// the whole snapshot also recovers from a lost response or receiver restart.
func (m *Publisher) Apply(ctx context.Context, snapshot neigh.NexthopCacheView) error {
	entries, count := snapshot.Entries()
	request := &operatorpb.SwapNeighboursRequest{
		Table:   m.tableName,
		Entries: make([]*operatorpb.NeighbourEntry, 0, count),
	}
	for entry := range entries {
		request.Entries = append(request.Entries, &operatorpb.NeighbourEntry{
			NextHop:      commonpb.NewIPAddressFromAddr(entry.NextHop),
			LinkAddr:     commonpb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
			HardwareAddr: commonpb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
			State:        operatorpb.NeighbourState(entry.State),
			UpdatedAt:    entry.UpdatedAt.Unix(),
			Priority:     entry.Priority,
			Device:       entry.HardwareRoute.Device,
		})
	}
	var failures error
	for _, target := range m.targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		attempt, cancel := context.WithTimeout(ctx, m.timeout)
		_, err := target.Client.SwapNeighbours(attempt, request)
		if status.Code(err) == codes.NotFound {
			_, err = target.Client.CreateTable(attempt, &operatorpb.CreateNeighbourTableRequest{
				Name: m.tableName, DefaultPriority: m.defaultPriority,
			})
			if err == nil {
				_, err = target.Client.SwapNeighbours(attempt, request)
			}
		}
		cancel()
		if err == nil {
			return nil
		}
		failures = errors.Join(failures, fmt.Errorf("gateway %q: %w", target.Name, err))
	}
	return failures
}

// Close releases all outgoing connections.
func (m *Publisher) Close() error {
	var failures error
	for _, target := range m.targets {
		failures = errors.Join(failures, target.Connection.Close())
	}
	return failures
}
