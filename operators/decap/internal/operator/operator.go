package operator

import (
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	decappb "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// NewOperator builds the decap operator: one target per configured
// function, its module config loaded once from the prefixes file.
func NewOperator(cfg *Config, options ...Option) (operator.Runnable, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	targets := make([]operator.StaticTarget, 0, len(cfg.Functions))
	for _, fn := range cfg.Functions {
		request, err := LoadModuleConfig(fn.PrefixesFile.Unwrap(), fn.Module.Unwrap())
		if err != nil {
			return nil, err
		}
		targets = append(targets, operator.StaticTarget{
			Name:    fn.Module.Unwrap(),
			Method:  decappb.DecapService_UpdateConfig_FullMethodName,
			Request: request,
			Function: &ynpb.Function{
				Id: &commonpb.FunctionId{Name: fn.Name.Unwrap()},
				Chains: []*ynpb.FunctionChain{{
					Chain: &ynpb.Chain{
						Name: fn.Chain.Unwrap(),
						Modules: []*commonpb.ModuleId{{
							Type: "decap",
							Name: fn.Module.Unwrap(),
						}},
					},
					Weight: fn.Weight.Unwrap(),
				}},
			},
			IgnorePdump: fn.IgnorePdump,
		})
	}

	return operator.NewStaticModuleOperator(
		"decap",
		operator.StaticConfig{
			Server:    cfg.Server,
			Gateways:  cfg.Gateways,
			Register:  cfg.Register,
			Reconcile: cfg.Reconcile,
		},
		targets,
		operator.WithStaticLog(opts.Log),
	)
}
