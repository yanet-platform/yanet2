package forward

import (
	"context"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	filterpbconv "github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

// RuleSet is the published payload: the accepted rule document together
// with its shared-memory representation.
type RuleSet struct {
	// Rules is the rule document exactly as accepted, echoed by reads.
	Rules []*forwardpb.Rule
	// Compiled is the rule document in the representation shared memory
	// publishes.
	Compiled []cforward.ForwardRule
}

// Backend abstracts shared memory operations.
type Backend interface {
	// Update builds a module config from the compiled rules and
	// publishes it to the dataplane.
	Update(name string, payload RuleSet) (crud.Handle, error)
	// Delete removes a module config.
	Delete(name string) error
	// ModuleCounters returns selected dataplane counters collected for a module config.
	// If counterNames is nil or empty, it returns all counters for the module config.
	ModuleCounters(name string, counterNames []string) []CounterView
}

type ForwardService struct {
	forwardpb.UnimplementedForwardServiceServer

	backend Backend
	core    *crud.Core[RuleSet]
}

func NewForwardService(backend Backend) *ForwardService {
	return &ForwardService{
		backend: backend,
		core:    crud.NewCore(backend),
	}
}

func (m *ForwardService) ListConfigs(
	ctx context.Context, request *forwardpb.ListConfigsRequest,
) (*forwardpb.ListConfigsResponse, error) {
	return &forwardpb.ListConfigsResponse{Configs: m.core.List()}, nil
}

func (m *ForwardService) ShowConfig(ctx context.Context, req *forwardpb.ShowConfigRequest) (*forwardpb.ShowConfigResponse, error) {
	ruleSet, ok := m.core.Get(req.GetName())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", req.GetName())
	}

	return &forwardpb.ShowConfigResponse{Name: req.Name, Rules: ruleSet.Rules}, nil
}

// materializeCounter builds the default counter name for a rule that left
// it empty: "to_" plus the target, bounded to the counter-name limit.
//
// The cut lands on a rune boundary because a name split mid-rune is not
// valid UTF-8, and a ShowConfig response carrying it would fail to marshal.
func materializeCounter(target string) string {
	name := "to_" + target
	if len(name) <= cforward.CounterNameMaxLen {
		return name
	}

	cut := cforward.CounterNameMaxLen
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return name[:cut]
}

// UpdateConfig replaces the rule set for a named forward module config and
// publishes it to the dataplane.
//
// A rule that leaves its counter empty is stored with "to_" plus its
// target, bounded to the counter-name limit, so ShowConfig never returns an
// empty name for a rule applied here. A non-empty counter is passed through
// verbatim.
func (m *ForwardService) UpdateConfig(ctx context.Context, req *forwardpb.UpdateConfigRequest) (*forwardpb.UpdateConfigResponse, error) {
	name := req.GetName()
	reqRules := req.Rules

	rules := make([]cforward.ForwardRule, 0, len(reqRules))
	for _, reqRule := range reqRules {
		action := reqRule.GetAction()

		// Assign onto the request's own action message: the ForwardRule
		// built below and the reqRules published as the payload after
		// the backend call both read this same action, so one assignment
		// here keeps the shared-memory write and ShowConfig in agreement.
		if action.Counter == "" {
			action.Counter = materializeCounter(action.Target)
		}

		devices, err := filterpbconv.ToDevices(reqRule.Devices)
		if err != nil {
			return nil, err
		}
		vlanRanges, err := filterpbconv.ToVlanRanges(reqRule.VlanRanges)
		if err != nil {
			return nil, err
		}
		src4s, err := filterpbconv.ToNet4sFromNetworks(reqRule.Sources4)
		if err != nil {
			return nil, err
		}
		dst4s, err := filterpbconv.ToNet4sFromNetworks(reqRule.Destinations4)
		if err != nil {
			return nil, err
		}
		src6s, err := filterpbconv.ToNet6sFromNetworks(reqRule.Sources6)
		if err != nil {
			return nil, err
		}
		dst6s, err := filterpbconv.ToNet6sFromNetworks(reqRule.Destinations6)
		if err != nil {
			return nil, err
		}

		rule := cforward.ForwardRule{
			Target:     action.Target,
			Mode:       cforward.ModeNone,
			Counter:    action.Counter,
			Devices:    devices,
			VlanRanges: vlanRanges,
			Src4s:      src4s,
			Dst4s:      dst4s,
			Src6s:      src6s,
			Dst6s:      dst6s,
		}

		if action.Mode == forwardpb.ForwardMode_IN {
			rule.Mode = cforward.ModeIn
		}
		if action.Mode == forwardpb.ForwardMode_OUT {
			rule.Mode = cforward.ModeOut
		}

		rules = append(rules, rule)
	}

	if err := m.core.Replace(name, RuleSet{Rules: reqRules, Compiled: rules}); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &forwardpb.UpdateConfigResponse{}, nil
}

func (m *ForwardService) DeleteConfig(ctx context.Context, req *forwardpb.DeleteConfigRequest) (*forwardpb.DeleteConfigResponse, error) {
	name := req.GetName()
	if err := m.core.Delete(name); err != nil {
		return nil, crud.DeleteError(name, err)
	}

	return &forwardpb.DeleteConfigResponse{}, nil
}
