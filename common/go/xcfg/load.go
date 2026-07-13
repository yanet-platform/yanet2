package xcfg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

type defaultable interface {
	Default()
}

type validatable interface {
	Validate() error
}

// Option configures LoadConfig and Decode.
type Option func(*options)

type options struct {
	KnownFields bool
}

func newOptions() *options {
	return &options{
		KnownFields: false,
	}
}

// WithKnownFields rejects YAML documents that contain keys not present in
// the destination type.
//
// Without this option an unknown key is silently dropped, which lets typos
// and renamed fields go unnoticed and leaves the corresponding field at its
// default value. With this option such a key becomes a decode error, so a
// stale or misspelled key in a deployed config fails loudly at startup
// instead of silently keeping a default that may be wrong for the
// environment. Strictness does not propagate into a field whose type
// implements a custom UnmarshalYAML that decodes via node.Decode, because
// yaml.v3 creates a fresh, non-strict decoder for that call.
func WithKnownFields() Option {
	return func(o *options) {
		o.KnownFields = true
	}
}

// LoadConfig reads a YAML file from path and returns the parsed Config.
//
// Default values are applied before unmarshalling so any absent field retains
// its default.
//
// Validation is driven by Decode, which calls Validate() on every field whose
// type implements it.
func LoadConfig[T any](path string, options ...Option) (*T, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := new(T)
	if def, ok := any(cfg).(defaultable); ok {
		def.Default()
	}
	if err := Decode(buf, cfg, options...); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}

// Decode deserializes YAML data into dst and then recursively validates all
// fields that implement "validatable".
func Decode(buf []byte, dst any, options ...Option) error {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	if opts.KnownFields {
		dec := yaml.NewDecoder(bytes.NewReader(buf))
		dec.KnownFields(true)
		if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	} else {
		if err := yaml.Unmarshal(buf, dst); err != nil {
			return err
		}
	}

	return validate(reflect.ValueOf(dst), "")
}

func validate(v reflect.Value, path string) error {
	// Dereference pointers.
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	// Check if the value itself implements validatable.
	if v.CanAddr() {
		if val, ok := v.Addr().Interface().(validatable); ok {
			if err := val.Validate(); err != nil {
				if path == "" {
					return err
				}
				return &PathError{Path: path, Err: err}
			}
		}
	}

	// Recurse into struct fields.
	if v.Kind() == reflect.Struct {
		t := v.Type()
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}

			name := yamlFieldName(f)
			fieldPath := name
			if path != "" {
				fieldPath = path + "." + name
			}

			if err := validate(v.Field(i), fieldPath); err != nil {
				return err
			}
		}
	}

	// Recurse into slice and array elements.
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		for idx := 0; idx < v.Len(); idx++ {
			if err := validate(v.Index(idx), fmt.Sprintf("%s[%d]", path, idx)); err != nil {
				return err
			}
		}
	}

	return nil
}

// yamlFieldName returns the YAML key name for a struct field.
func yamlFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	if tag == "" {
		return f.Name
	}

	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return f.Name
	}

	return name
}
