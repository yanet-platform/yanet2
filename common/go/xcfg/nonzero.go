package xcfg

import (
	"fmt"

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
