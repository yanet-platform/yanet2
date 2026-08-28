package operator

import (
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

// NewOperator builds the forward operator: one target per configured
// function, its rules loaded once from the rules file.
func NewOperator(cfg *Config, options ...Option) (operator.Runnable, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	targets := make([]operator.StaticTarget, 0, len(cfg.Functions))
	for _, fn := range cfg.Functions {
		rules, err := LoadForwardRules(fn.RulesFile.Unwrap())
		if err != nil {
			return nil, fmt.Errorf("failed to load rules file %q: %w", fn.RulesFile.Unwrap(), err)
		}
		targets = append(targets, operator.StaticTarget{
			Name:   fn.Module.Unwrap(),
			Method: forwardpb.ForwardService_UpdateConfig_FullMethodName,
			Request: &forwardpb.UpdateConfigRequest{
				Name:  fn.Module.Unwrap(),
				Rules: rules,
			},
			Function: &ynpb.Function{
				Id: &commonpb.FunctionId{Name: fn.Name.Unwrap()},
				Chains: []*ynpb.FunctionChain{{
					Chain: &ynpb.Chain{
						Name: fn.Chain.Unwrap(),
						Modules: []*commonpb.ModuleId{{
							Type: "forward",
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
		"forward",
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
