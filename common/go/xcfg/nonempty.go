package xcfg

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// NonEmptyString is a wrapper around string that ensures the string is not
// empty.
type NonEmptyString struct {
	v string
}

// MustNonEmptyString creates a new NonEmptyString, panicking if the string is
// empty.
func MustNonEmptyString(v string) NonEmptyString {
	m, err := NewNonEmptyString(v)
	if err != nil {
		panic(err)
	}

	return m
}

// NewNonEmptyString creates a new NonEmptyString, returning an error if the
// string is empty.
func NewNonEmptyString(v string) (NonEmptyString, error) {
	m := NonEmptyString{v: v}
	if err := m.Validate(); err != nil {
		return NonEmptyString{}, err
	}

	return m, nil
}

// Unwrap returns the underlying string.
func (m NonEmptyString) Unwrap() string {
	return m.v
}

// Validate checks that the string is not empty.
func (m NonEmptyString) Validate() error {
	if len(m.v) == 0 {
		return fmt.Errorf("non-empty string is required")
	}

	return nil
}

// String implements fmt.Stringer.
func (m NonEmptyString) String() string {
	return m.v
}

// MarshalYAML implements yaml.Marshaler.
func (m NonEmptyString) MarshalYAML() (any, error) {
	return m.v, nil
}

// UnmarshalYAML implements yaml.Unmarshaler, rejecting empty strings.
func (m *NonEmptyString) UnmarshalYAML(node *yaml.Node) error {
	var out string
	if err := node.Decode(&out); err != nil {
		return fmt.Errorf("failed to decode non-empty string: %w", err)
	}

	n, err := NewNonEmptyString(out)
	if err != nil {
		return &LineError{Line: node.Line, Err: err}
	}

	*m = n
	return nil
}
