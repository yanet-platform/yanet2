package mirror

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	filterpbconv "github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/modules/mirror/bindings/go/cmirror"
	mirrorpb "github.com/yanet-platform/yanet2/modules/mirror/controlplane/mirrorpb/v1"
)

// RuleSet is the published payload: the accepted rule document together
// with its shared-memory representation.
type RuleSet struct {
	// Rules is the rule document exactly as accepted, echoed by reads.
	Rules []*mirrorpb.Rule
	// Compiled is the rule document in the representation shared memory
	// publishes.
	Compiled []cmirror.MirrorRule
}

// Backend abstracts shared memory operations.
type Backend interface {
	// Update builds a module config from the compiled rules and
	// publishes it to the dataplane.
	Update(name string, payload RuleSet) (crud.Handle, error)
	// Delete removes a module config.
	Delete(name string) error
}

type MirrorService struct {
	mirrorpb.UnimplementedMirrorServiceServer

	core *crud.Core[RuleSet]
}

func NewMirrorService(backend Backend) *MirrorService {
	return &MirrorService{
		core: crud.NewCore(backend),
	}
}

func (m *MirrorService) ListConfigs(
	ctx context.Context, request *mirrorpb.ListConfigsRequest,
) (*mirrorpb.ListConfigsResponse, error) {
	return &mirrorpb.ListConfigsResponse{Configs: m.core.List()}, nil
}

func (m *MirrorService) ShowConfig(
	ctx context.Context,
	req *mirrorpb.ShowConfigRequest,
) (*mirrorpb.ShowConfigResponse, error) {
	ruleSet, ok := m.core.Get(req.GetName())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", req.GetName())
	}

	return &mirrorpb.ShowConfigResponse{Name: req.Name, Rules: ruleSet.Rules}, nil
}

func (m *MirrorService) UpdateConfig(
	ctx context.Context,
	req *mirrorpb.UpdateConfigRequest,
) (*mirrorpb.UpdateConfigResponse, error) {
	name := req.GetName()
	reqRules := req.GetRules()

	rules := make([]cmirror.MirrorRule, 0, len(reqRules))
	for _, reqRule := range reqRules {
		action := reqRule.GetAction()

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

		rule := cmirror.MirrorRule{
			Target:     action.Target,
			Mode:       cmirror.ModeNone,
			Counter:    action.Counter,
			Devices:    devices,
			VlanRanges: vlanRanges,
			Src4s:      src4s,
			Dst4s:      dst4s,
			Src6s:      src6s,
			Dst6s:      dst6s,
		}

		if action.Mode == mirrorpb.MirrorMode_IN {
			rule.Mode = cmirror.ModeIn
		}
		if action.Mode == mirrorpb.MirrorMode_OUT {
			rule.Mode = cmirror.ModeOut
		}

		rules = append(rules, rule)
	}

	if err := m.core.Replace(name, RuleSet{Rules: reqRules, Compiled: rules}); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	return &mirrorpb.UpdateConfigResponse{}, nil
}

func (m *MirrorService) DeleteConfig(
	ctx context.Context,
	req *mirrorpb.DeleteConfigRequest,
) (*mirrorpb.DeleteConfigResponse, error) {
	name := req.GetName()
	if err := m.core.Delete(name); err != nil {
		return nil, crud.DeleteError(name, err)
	}

	return &mirrorpb.DeleteConfigResponse{}, nil
}
