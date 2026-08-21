package xcfg

import (
	"encoding/json"
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// Optional is a wrapper around a value that may be absent from the source
// document entirely, distinguishing that absence from a present zero value.
type Optional[T any] struct {
	v *T
}

// NewOptional wraps v as present.
func NewOptional[T any](v T) Optional[T] {
	return Optional[T]{v: &v}
}

// Unwrap returns the underlying value, or nil if the key was absent from
// the document.
func (m Optional[T]) Unwrap() *T {
	return m.v
}

// String implements fmt.Stringer.
func (m Optional[T]) String() string {
	if m.v == nil {
		return "<absent>"
	}
	return fmt.Sprint(*m.v)
}

// MarshalYAML implements yaml.Marshaler for round-trip serialization.
func (m Optional[T]) MarshalYAML() (any, error) {
	return m.v, nil
}

// MarshalJSON implements json.Marshaler.
//
// Without it, config dumps reach a reflect-based JSON encoder that finds
// no exported field and renders any value as "{}".
func (m Optional[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.v)
}

// UnmarshalYAML implements yaml.Unmarshaler.
//
// An absent key never calls this method, so m.v stays nil. A present one
// seeds a new T with its Default before decoding, so an omitted field
// within it keeps that default.
func (m *Optional[T]) UnmarshalYAML(node *yaml.Node) error {
	v := new(T)
	if d, ok := any(v).(defaultable); ok {
		d.Default()
	}
	if err := node.Decode(v); err != nil {
		return err
	}
	m.v = v
	return nil
}

// WalkType implements walkableType, letting the unknown-key walker in
// known_keys.go see through Optional to check T's own fields instead of
// treating it as opaque.
func (m Optional[T]) WalkType() reflect.Type {
	return reflect.TypeFor[T]()
}

// EnvType implements envTyped, letting the environment overlay in env.go
// address the wrapped value's own fields.
//
// An override materialises the optional value: naming a key under an absent
// Optional makes it present, since the overlay writes the key into the
// document and UnmarshalYAML then seeds T with its Default.
func (m Optional[T]) EnvType() reflect.Type {
	return reflect.TypeFor[T]()
}

// EnvValue implements envValued, exposing a present wrapped default to the
// environment overlay.
func (m Optional[T]) EnvValue() reflect.Value {
	if m.v == nil {
		return reflect.Value{}
	}
	return reflect.ValueOf(m.v).Elem()
}

// Elem implements validatableElem, letting validate in load.go recurse into
// the wrapped value's own fields under Optional's own dotted path.
//
// An absent Optional returns a zero Value, which validate treats as
// nothing to check.
func (m Optional[T]) Elem() reflect.Value {
	if m.v == nil {
		return reflect.Value{}
	}
	return reflect.ValueOf(m.v).Elem()
}
