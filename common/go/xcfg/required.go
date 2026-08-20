package xcfg

import (
	"encoding/json"
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// Required is a wrapper around a value that ensures it was set explicitly in
// the source document, distinguishing an explicit zero value from an absent
// one.
type Required[T any] struct {
	v   T
	set bool
}

// NewRequired creates a new Required, marking it as explicitly set.
func NewRequired[T any](v T) Required[T] {
	return Required[T]{v: v, set: true}
}

// Unwrap returns the underlying value.
func (m Required[T]) Unwrap() T {
	return m.v
}

// String implements fmt.Stringer.
func (m Required[T]) String() string {
	return fmt.Sprint(m.v)
}

// EnvType implements envTyped, so a required value may be supplied from the
// environment. The override goes through UnmarshalYAML like any other value,
// which is what marks it explicitly set.
func (m Required[T]) EnvType() reflect.Type {
	return reflect.TypeFor[T]()
}

// EnvValue implements envValued, exposing the current wrapped default to the
// environment overlay.
func (m Required[T]) EnvValue() reflect.Value {
	return reflect.ValueOf(m.v)
}

// EnvIsSet reports whether the wrapped value was supplied explicitly.
func (m Required[T]) EnvIsSet() bool {
	return m.set
}

// Elem exposes the wrapped value for recursive validation.
func (m *Required[T]) Elem() reflect.Value {
	return reflect.ValueOf(&m.v).Elem()
}

// Validate checks that the value was set explicitly.
func (m Required[T]) Validate() error {
	if !m.set {
		return fmt.Errorf("value must be set explicitly")
	}

	return nil
}

// MarshalYAML implements yaml.Marshaler for round-trip serialization.
func (m Required[T]) MarshalYAML() (any, error) {
	return m.v, nil
}

// MarshalJSON implements json.Marshaler.
//
// Without it, config dumps reach a reflect-based JSON encoder that finds
// no exported field and renders any value as "{}".
func (m Required[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.v)
}

// UnmarshalYAML implements yaml.Unmarshaler, recording that a value was
// decoded.
func (m *Required[T]) UnmarshalYAML(node *yaml.Node) error {
	var out T
	if err := node.Decode(&out); err != nil {
		return fmt.Errorf("failed to decode required value: %w", err)
	}

	*m = Required[T]{v: out, set: true}
	return nil
}
