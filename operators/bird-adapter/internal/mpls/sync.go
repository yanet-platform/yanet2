package mpls

import (
	"context"
	"net/netip"

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
	Log *zap.Logger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
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
// route-mpls on the next push, keeping the ones a failed push could not
// deliver. When route-mpls does not know the config, as after a
// control-plane restart, it sends the whole table instead, so the
// dataplane never runs a table built from changes alone.
type Sync struct {
	name     string
	client   configClient
	sourceV4 netip.Addr
	sourceV6 netip.Addr
	rib      Rib
	// pending holds the changes route-mpls has not acknowledged yet.
	pending []*routemplspb.UpdateEvent
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
		log:      opts.Log,
	}
}

// Apply feeds one route into the RIB and queues the route-mpls events for
// the best-path changes it causes.
//
// A route that cannot be expressed as a rule is logged and left out.
func (m *Sync) Apply(route rib.Route) {
	for _, update := range m.rib.Apply(route) {
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
// route-mpls does not know the config.
//
// Changes a failed push could not deliver stay queued for the next one,
// except those route-mpls rejects as invalid, which no retry delivers.
func (m *Sync) Push(ctx context.Context) error {
	if len(m.pending) == 0 {
		return nil
	}

	_, err := m.client.UpdateConfig(ctx, &routemplspb.UpdateConfigRequest{
		Name:    m.name,
		Updates: m.pending,
	})
	if status.Code(err) == codes.NotFound {
		m.log.Info("route-mpls does not know the config, sending the whole table",
			zap.String("config", m.name),
		)
		_, err = m.client.CreateConfig(ctx, &routemplspb.CreateConfigRequest{
			Name:  m.name,
			Rules: m.table(),
		})
	}
	if err == nil || status.Code(err) == codes.InvalidArgument {
		m.pending = nil
	}
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
