package xcfg

import (
	"encoding/json"
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// Numeric constrains to types with an underlying numeric representation.
type Numeric interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

// NonZero is a wrapper around a numeric type that ensures the value is not
// zero.
type NonZero[T Numeric] struct {
	v T
}

// MustNonZero creates a new NonZero, panicking if the value is zero.
func MustNonZero[T Numeric](v T) NonZero[T] {
	m, err := NewNonZero(v)
	if err != nil {
		panic(err)
	}

	return m
}

// NewNonZero creates a new NonZero.
func NewNonZero[T Numeric](v T) (NonZero[T], error) {
	m := NonZero[T]{v: v}
	if err := m.Validate(); err != nil {
		return NonZero[T]{}, err
	}

	return m, nil
}

// Unwrap returns the underlying value.
func (m NonZero[T]) Unwrap() T {
	return m.v
}

// String implements fmt.Stringer.
func (m NonZero[T]) String() string {
	return fmt.Sprint(m.v)
}

// EnvType implements envTyped, letting the environment overlay in env.go
// configure the wrapped value instead of treating the wrapper as opaque.
func (m NonZero[T]) EnvType() reflect.Type {
	return reflect.TypeFor[T]()
}

// EnvValue implements envValued, exposing the current wrapped default to the
// environment overlay.
func (m NonZero[T]) EnvValue() reflect.Value {
	return reflect.ValueOf(m.v)
}

// Validate checks that the value is not zero.
func (m NonZero[T]) Validate() error {
	if m.v == 0 {
		return fmt.Errorf("non-zero value is required")
	}

	return nil
}

// MarshalYAML implements yaml.Marshaler for round-trip serialization.
func (m NonZero[T]) MarshalYAML() (any, error) {
	return m.v, nil
}

// MarshalJSON implements json.Marshaler.
//
// Without it, config dumps reach a reflect-based JSON encoder that finds
// no exported field and renders any value as "{}".
func (m NonZero[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.v)
}

// UnmarshalYAML implements yaml.Unmarshaler, rejecting zero values.
func (m *NonZero[T]) UnmarshalYAML(node *yaml.Node) error {
	var out T
	if err := node.Decode(&out); err != nil {
		return fmt.Errorf("failed to decode non-zero value: %w", err)
	}

	n, err := NewNonZero(out)
	if err != nil {
		return &LineError{Line: node.Line, Err: err}
	}

	*m = n
	return nil
}
