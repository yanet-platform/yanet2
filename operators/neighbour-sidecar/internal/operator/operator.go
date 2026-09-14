package operator

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

type options struct {
	Log *zap.Logger
}

func newOptions() *options {
	return &options{Log: zap.NewNop()}
}

// Option configures sidecar logging.
type Option func(*options)

// WithLog selects the logger shared by discovery and publication.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

// NewOperator connects the shared kernel monitor to a snapshot publisher.
func NewOperator(cfg *Config, options ...Option) (*operator.Operator[neigh.NexthopCacheView], error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	table := neigh.NewNeighTable()
	kernel, err := table.CreateSource(cfg.TableName, cfg.DefaultPriority, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create neighbour source: %w", err)
	}
	publisher, err := NewPublisher(cfg)
	if err != nil {
		return nil, err
	}
	source := NewNeighbourSource(table)
	monitor := neigh.NewNeighMonitor(table, kernel,
		neigh.WithLog(opts.Log),
		neigh.WithLinkMap(cfg.LinkMap),
		neigh.WithUpdateInterval(cfg.UpdateInterval),
		neigh.WithOnHealthy(source.OnHealthy),
	)
	return operator.NewOperator(
		publisher,
		source,
		operator.WithLog(opts.Log),
		operator.WithReconcile(cfg.Reconcile),
		operator.WithWorkers(monitor.Run),
	), nil
}
