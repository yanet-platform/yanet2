// Package operator implements the generic operator: an instance pushes the
// module configs and functions its YAML config spells to every gateway.
package operator

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

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
		if err := checkFunctionReference(target, request); err != nil {
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

// checkFunctionReference refuses a target whose function does not reference
// the module config its file names.
//
// The file and the function are independent documents, so a typo in either
// would otherwise surface only as an endless reconcile retry.
func checkFunctionReference(target TargetConfig, request proto.Message) error {
	function := target.Function.Unwrap()
	if function == nil {
		return nil
	}
	name, ok := requestConfigName(request)
	if !ok {
		return nil
	}
	if name == "" {
		return fmt.Errorf(
			"module config %q names no config, spell its name in the file",
			target.File.Unwrap(),
		)
	}
	for _, chain := range function.GetChains() {
		for _, module := range chain.GetChain().GetModules() {
			if module.GetName() == name {
				return nil
			}
		}
	}
	return fmt.Errorf(
		"function %q does not reference config %q pushed by this target",
		function.GetId().GetName(), name,
	)
}

// requestConfigName returns the request's name field, ok=false when the
// message declares none.
func requestConfigName(request proto.Message) (string, bool) {
	descriptor := request.ProtoReflect().Descriptor().Fields().ByName("name")
	if descriptor == nil || descriptor.Kind() != protoreflect.StringKind ||
		descriptor.IsList() || descriptor.IsMap() {
		return "", false
	}
	return request.ProtoReflect().Get(descriptor).String(), true
}
