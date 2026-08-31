// Package operator implements the generic operator: an instance pushes the
// module configs and functions its YAML config spells to every gateway.
package operator

import (
	"fmt"

	"github.com/yanet-platform/yanet2/common/go/operator"
)

// NewOperator builds one static operator instance: every target's module
// config is loaded once from its file and pushed as is.
func NewOperator(cfg *Config, options ...Option) (operator.Runnable, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	targets := make([]operator.StaticTarget, 0, len(cfg.Targets))
	for idx, target := range cfg.Targets {
		request, err := LoadRequest(target.Method.Unwrap(), target.File.Unwrap())
		if err != nil {
			return nil, fmt.Errorf("target %d: %w", idx, err)
		}
		targets = append(targets, operator.StaticTarget{
			Name:        target.Name,
			Method:      target.Method.Unwrap(),
			Request:     request,
			Function:    target.Function.Unwrap(),
			IgnorePdump: target.IgnorePdump,
		})
	}

	return operator.NewStaticModuleOperator(
		cfg.Name.Unwrap(),
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
