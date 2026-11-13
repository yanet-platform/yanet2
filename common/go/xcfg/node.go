package xcfg

import (
	"reflect"
	"strconv"

	"gopkg.in/yaml.v3"
)

var deferredNodeType = reflect.TypeFor[Node]()

// Node holds configuration whose destination type is selected after loading.
//
// Use it for a type/config pair decoded by a factory. LoadConfig and Decode
// retain the node's environment prefix, environment snapshot and validation
// options. Its Decode method uses those settings once the factory supplies
// a destination, without reading the process environment again.
type Node struct {
	value   yaml.Node
	options options
	env     map[string]string
}

func newNode(value Node, opts *options, prefix string, env map[string]string) Node {
	m := value
	m.options = *opts
	m.options.EnvPrefix = prefix
	m.env = env
	return m
}

// Decode fills and validates a destination with the captured loader options.
func (m *Node) Decode(dst any) error {
	encoded, err := yaml.Marshal(&m.value)
	if err != nil {
		return err
	}
	return decode(encoded, dst, &m.options, m.env)
}

// Encode replaces the node with a Go value and clears any captured overrides.
func (m *Node) Encode(value any) error {
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return err
	}
	*m = Node{value: node}
	return nil
}

// MarshalYAML serializes the source value without the captured environment.
func (m Node) MarshalYAML() (any, error) {
	return &m.value, nil
}

// UnmarshalYAML retains raw settings until their destination type is known.
func (m *Node) UnmarshalYAML(node *yaml.Node) error {
	*m = Node{value: *node}
	return nil
}

// prepareNodes binds deferred settings before validating their containing
// config, whose factory may decode them. Prefixes follow the env walk.
func prepareNodes(value reflect.Value, opts *options, prefix string, env map[string]string) {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	if value.Type() == yamlNodeType {
		return
	}
	if value.CanAddr() {
		if node, ok := reflect.TypeAssert[*Node](value.Addr()); ok {
			*node = newNode(*node, opts, prefix, env)
			return
		}
		if wrapper, ok := reflect.TypeAssert[validatableElem](value.Addr()); ok {
			wrapped := wrapper.Elem()
			if wrapped.IsValid() {
				prepareNodes(wrapped, opts, prefix, env)
			}
			return
		}
	}

	switch value.Kind() {
	case reflect.Struct:
		for idx := range value.Type().NumField() {
			field := value.Type().Field(idx)
			if !field.IsExported() || field.Tag.Get("yaml") == "-" {
				continue
			}
			fieldPrefix := prefix
			if !isInlineTag(field.Tag.Get("yaml")) {
				fieldPrefix = envJoin(prefix, envSegment(envKeyName(field)))
			}
			prepareNodes(value.Field(idx), opts, fieldPrefix, env)
		}
	case reflect.Slice, reflect.Array:
		for idx := range value.Len() {
			prepareNodes(value.Index(idx), opts, envJoin(prefix, strconv.Itoa(idx)), env)
		}
	}
}
