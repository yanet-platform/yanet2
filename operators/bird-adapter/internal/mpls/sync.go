package mpls

import (
	"context"
	"net/netip"
	"time"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/routemplspb/v1"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/rib"
)

// configClient is the part of the route-mpls client a Sync uses.
type configClient interface {
	UpdateConfig(
		ctx context.Context,
		in *routemplspb.UpdateConfigRequest,
		opts ...grpc.CallOption,
	) (*routemplspb.UpdateConfigResponse, error)
	CreateConfig(
		ctx context.Context,
		in *routemplspb.CreateConfigRequest,
		opts ...grpc.CallOption,
	) (*routemplspb.CreateConfigResponse, error)
}

// Option configures the Sync constructor.
type Option func(*options)

type options struct {
	RetryBackOff backoff.BackOff
	Log          *zap.Logger
}

func newOptions() *options {
	return &options{
		RetryBackOff: &backoff.ExponentialBackOff{
			InitialInterval:     backoff.DefaultInitialInterval,
			RandomizationFactor: backoff.DefaultRandomizationFactor,
			Multiplier:          backoff.DefaultMultiplier,
			MaxInterval:         30 * time.Second,
		},
		Log: zap.NewNop(),
	}
}

// WithRetryBackOff sets the delays a Sync waits out after each failed push
// before it pushes again.
func WithRetryBackOff(retry backoff.BackOff) Option {
	return func(o *options) {
		o.RetryBackOff = retry
	}
}

// WithLog sets the logger of a Sync.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

// Sync keeps one route-mpls config in step with the MPLS routes BIRD
// exports.
//
// It queues the best-path changes of each route and delivers them to
// route-mpls on the next push. When route-mpls does not know the config, as
// after a control-plane restart, or a push fails, it drops the queue and
// sends the whole table instead. The dataplane thus never runs a table
// built from changes alone, and a route-mpls outage holds no memory beyond
// the routes themselves. After a failure the pushes wait out a growing
// delay.
type Sync struct {
	name     string
	client   configClient
	sourceV4 netip.Addr
	sourceV6 netip.Addr
	rib      Rib
	// pending holds the changes queued since the last push.
	pending []*routemplspb.UpdateEvent
	// resync makes the next push send the whole table instead of the
	// queued changes.
	resync bool
	// retry yields the delay after each failed push, and retryAt is when
	// the next push is due.
	retry   backoff.BackOff
	retryAt time.Time
	log     *zap.Logger
}

// NewSync returns a Sync for the named route-mpls config whose tunnels
// start at the given source addresses.
func NewSync(
	name string,
	client configClient,
	sourceV4 netip.Addr,
	sourceV6 netip.Addr,
	options ...Option,
) *Sync {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	return &Sync{
		name:     name,
		client:   client,
		sourceV4: sourceV4,
		sourceV6: sourceV6,
		rib:      NewRib(),
		retry:    opts.RetryBackOff,
		log:      opts.Log,
	}
}

// Apply feeds one route into the RIB and queues the route-mpls events for
// the best-path changes it causes.
//
// Nothing is queued while the whole table is due. A route that cannot be
// expressed as a rule is logged and left out.
func (m *Sync) Apply(route rib.Route) {
	updates := m.rib.Apply(route)
	if m.resync {
		return
	}

	for _, update := range updates {
		rule, err := m.rule(&update)
		if err != nil {
			continue
		}
		if update.ToRemove {
			m.pending = append(m.pending, &routemplspb.UpdateEvent{
				Event: &routemplspb.UpdateEvent_Withdraw{Withdraw: rule},
			})
			continue
		}
		m.pending = append(m.pending, &routemplspb.UpdateEvent{
			Event: &routemplspb.UpdateEvent_Update{Update: rule},
		})
	}
}

// Push delivers the queued changes to route-mpls, or the whole table when
// route-mpls does not know the config or the previous push failed.
//
// A failed push drops the queued changes, so the next one sends the whole
// table, and makes the pushes do nothing until its delay has passed;
// callers therefore push as often as they like.
func (m *Sync) Push(ctx context.Context) error {
	if time.Now().Before(m.retryAt) {
		return nil
	}

	var err error
	switch {
	case m.resync:
		err = m.pushTable(ctx)
	case len(m.pending) > 0:
		err = m.pushChanges(ctx)
	default:
		return nil
	}

	m.pending = nil
	if err != nil {
		m.resync = true
		m.retryAt = time.Now().Add(m.retry.NextBackOff())
		return err
	}

	m.resync = false
	m.retry.Reset()
	return nil
}

// pushChanges sends the queued changes, or the whole table when route-mpls
// does not know the config.
func (m *Sync) pushChanges(ctx context.Context) error {
	_, err := m.client.UpdateConfig(ctx, &routemplspb.UpdateConfigRequest{
		Name:    m.name,
		Updates: m.pending,
	})
	if status.Code(err) != codes.NotFound {
		return err
	}

	m.log.Info("route-mpls does not know the config, sending the whole table",
		zap.String("config", m.name),
	)
	return m.pushTable(ctx)
}

// pushTable replaces the config with the rules of every best route the RIB
// holds.
func (m *Sync) pushTable(ctx context.Context) error {
	_, err := m.client.CreateConfig(ctx, &routemplspb.CreateConfigRequest{
		Name:  m.name,
		Rules: m.table(),
	})
	return err
}

// table returns the rules of every best route the RIB holds.
func (m *Sync) table() []*routemplspb.Rule {
	routes := m.rib.Best()
	rules := make([]*routemplspb.Rule, 0, len(routes))
	for idx := range routes {
		rule, err := m.rule(&routes[idx])
		if err != nil {
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

// rule converts a route to a route-mpls rule, logging a route it cannot
// convert.
func (m *Sync) rule(route *rib.Route) (*routemplspb.Rule, error) {
	rule, err := rib.ToPBMPLSRoute(route, m.sourceV4, m.sourceV6)
	if err != nil {
		m.log.Debug("mpls route cannot be converted to protobuf, skip",
			zap.String("prefix", route.Prefix.String()),
			zap.Error(err),
		)
	}
	return rule, err
}
