// Package operator implements the generic operator: an instance pushes the
// module configs and functions its YAML config spells to every gateway.
package operator

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/yanet-platform/yanet2/common/go/operator"
)

// moduleKey identifies a module config by normalized type and name.
type moduleKey struct {
	moduleType string
	name       string
}

// NewOperator builds one generic operator instance: every module config is
// loaded once from its file, and the functions are published after them.
func NewOperator(cfg *Config, options ...Option) (operator.Runnable, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	served := map[string]bool{}
	pushed := map[moduleKey]bool{}
	targets := make([]operator.StaticTarget, 0, len(cfg.Configs)+len(cfg.Functions))
	for idx, config := range cfg.Configs {
		request, err := LoadRequest(config.Method.Unwrap(), config.File.Unwrap())
		if err != nil {
			return nil, fmt.Errorf("configs[%d]: %w", idx, err)
		}
		if err := bindRequestName(request, config); err != nil {
			return nil, fmt.Errorf("configs[%d]: %w", idx, err)
		}
		if moduleType, ok := methodModuleType(config.Method.Unwrap()); ok {
			served[moduleType] = true
			pushed[moduleKey{moduleType: moduleType, name: config.Name.Unwrap()}] = true
		}
		targets = append(targets, operator.StaticTarget{
			Name:    config.Name.Unwrap(),
			Method:  config.Method.Unwrap(),
			Request: request,
		})
	}

	for idx, function := range cfg.Functions {
		if err := checkFunctionReferences(function, served, pushed); err != nil {
			return nil, fmt.Errorf("functions[%d]: %w", idx, err)
		}
		targets = append(targets, operator.StaticTarget{
			Name:        function.Name.Unwrap(),
			Function:    function.AsFunction(),
			IgnorePdump: function.IgnorePdump,
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

// checkFunctionReferences refuses a function that references a module of a
// type this instance serves without the instance pushing that config.
//
// Such a reference is a typo in either section, and would otherwise
// surface only as an endless reconcile retry, or silently wire a stale
// same-named config. Modules of other types are referenced freely, they
// belong to other operators.
func checkFunctionReferences(
	function FunctionConfig,
	served map[string]bool,
	pushed map[moduleKey]bool,
) error {
	for _, chain := range function.Chains {
		for _, module := range chain.Chain.Modules {
			moduleType := normalizeModuleType(module.Type.Unwrap())
			if !served[moduleType] {
				continue
			}
			if !pushed[moduleKey{moduleType: moduleType, name: module.Name.Unwrap()}] {
				return fmt.Errorf(
					"function %q references %s config %q, which this instance does not push",
					function.Name.Unwrap(), module.Type.Unwrap(), module.Name.Unwrap(),
				)
			}
		}
	}
	return nil
}

// bindRequestName fills the request's config-naming field with the
// entry's name, refusing a file that names another config.
func bindRequestName(request proto.Message, config ModuleConfig) error {
	message := request.ProtoReflect()
	descriptor, ok := configNameField(message.Descriptor())
	if !ok {
		return nil
	}
	name := config.Name.Unwrap()
	switch got := message.Get(descriptor).String(); got {
	case "":
		message.Set(descriptor, protoreflect.ValueOfString(name))
	case name:
	default:
		return fmt.Errorf(
			"module config %q names config %q, but the entry is named %q",
			config.File.Unwrap(), got, name,
		)
	}
	return nil
}

// configNameField finds the request's config-naming field, ok=false when
// the message declares none.
//
// The tree spells it as name in module update requests and as module_name
// in the route FIB request.
func configNameField(descriptor protoreflect.MessageDescriptor) (protoreflect.FieldDescriptor, bool) {
	for _, field := range []protoreflect.Name{"name", "module_name"} {
		found := descriptor.Fields().ByName(field)
		if found == nil || found.Kind() != protoreflect.StringKind ||
			found.IsList() || found.IsMap() {
			continue
		}
		return found, true
	}
	return nil, false
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
