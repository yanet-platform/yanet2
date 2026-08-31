// Package operator implements the generic operator: an instance pushes the
// module configs and functions its YAML config spells to every gateway.
package operator

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/yanet-platform/yanet2/common/go/operator"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
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
		function := target.Function.AsFunction()
		if err := checkFunctionReference(target, function, request); err != nil {
			return nil, fmt.Errorf("target %d: %w", idx, err)
		}
		targets = append(targets, operator.StaticTarget{
			Name:        target.Name,
			Method:      target.Method.Unwrap(),
			Request:     request,
			Function:    function,
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
// would otherwise surface only as an endless reconcile retry, or silently
// wire a same-named config of another module.
func checkFunctionReference(target TargetConfig, function *ynpb.Function, request proto.Message) error {
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
	moduleType, typed := methodModuleType(target.Method.Unwrap())
	for _, chain := range function.GetChains() {
		for _, module := range chain.GetChain().GetModules() {
			if module.GetName() != name {
				continue
			}
			if typed && normalizeModuleType(module.GetType()) != moduleType {
				continue
			}
			return nil
		}
	}
	return fmt.Errorf(
		"function %q does not reference config %q pushed by this target",
		function.GetId().GetName(), name,
	)
}

// requestConfigName returns the request's config-naming field, ok=false
// when the message declares none.
//
// The tree spells it as name in module update requests and as module_name
// in the route FIB request.
func requestConfigName(request proto.Message) (string, bool) {
	message := request.ProtoReflect()
	for _, field := range []protoreflect.Name{"name", "module_name"} {
		descriptor := message.Descriptor().Fields().ByName(field)
		if descriptor == nil || descriptor.Kind() != protoreflect.StringKind ||
			descriptor.IsList() || descriptor.IsMap() {
			continue
		}
		return message.Get(descriptor).String(), true
	}
	return "", false
}

// methodModuleType returns the module type a modules.* method serves,
// ok=false for any other package.
func methodModuleType(method string) (string, bool) {
	service, _, ok := strings.Cut(strings.TrimPrefix(method, "/"), "/")
	if !ok {
		return "", false
	}
	segments := strings.SplitN(service, ".", 3)
	if len(segments) < 3 || segments[0] != "modules" {
		return "", false
	}
	return normalizeModuleType(segments[1]), true
}

// normalizeModuleType folds a proto package segment and a dataplane
// module type into one spelling, such as route_mpls versus route-mpls.
func normalizeModuleType(moduleType string) string {
	return strings.ReplaceAll(moduleType, "-", "_")
}
