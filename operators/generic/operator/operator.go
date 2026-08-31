// Package operator implements the generic operator: an instance pushes the
// module configs and functions its YAML config spells to every gateway.
package operator

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/yanet-platform/yanet2/common/go/operator"
)

// NewOperator builds one generic operator instance: every module config is
// loaded once from its file, and the functions are published after them.
func NewOperator(cfg *Config, options ...Option) (operator.Runnable, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	targets := make([]operator.StaticTarget, 0, len(cfg.Configs)+len(cfg.Functions))
	for idx, config := range cfg.Configs {
		request, err := LoadRequest(config.Method.Unwrap(), config.File.Unwrap())
		if err != nil {
			return nil, fmt.Errorf("configs[%d]: %w", idx, err)
		}
		if err := bindRequestName(request, config); err != nil {
			return nil, fmt.Errorf("configs[%d]: %w", idx, err)
		}
		targets = append(targets, operator.StaticTarget{
			Name:    config.Name.Unwrap(),
			Method:  config.Method.Unwrap(),
			Request: request,
		})
	}

	for _, function := range cfg.Functions {
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
// The tree spells it as name in module update requests, module_name in
// the route FIB request and config_name in the balancer one.
func configNameField(descriptor protoreflect.MessageDescriptor) (protoreflect.FieldDescriptor, bool) {
	for _, field := range []protoreflect.Name{"name", "module_name", "config_name"} {
		found := descriptor.Fields().ByName(field)
		if found == nil || found.Kind() != protoreflect.StringKind ||
			found.IsList() || found.IsMap() {
			continue
		}
		return found, true
	}
	return nil, false
}
