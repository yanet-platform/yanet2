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

// validatableElem is implemented by a wrapper whose validation lives on the
// value it wraps rather than on the wrapper itself, such as Optional[T],
// letting validate recurse into that value under the wrapper's own dotted
// path instead of stopping at the wrapper's unexported field.
type validatableElem interface {
	Elem() reflect.Value
}

// Option configures LoadConfig and Decode.
type Option func(*options)

type options struct {
	KnownFields bool
	Env         bool
	EnvPrefix   string
}

func newOptions() *options {
	return &options{
		KnownFields: false,
		EnvPrefix:   DefaultEnvPrefix,
	}
}

// WithKnownFields rejects YAML documents that contain keys not present in
// the destination type.
//
// Without this option an unknown key is silently dropped, letting typos and
// renamed fields go unnoticed while the corresponding field silently keeps
// its default. With this option such a key becomes a decode error, including
// for a key nested under a field whose type implements a custom
// UnmarshalYAML — see CheckKnownKeys for the mechanism.
func WithKnownFields() Option {
	return func(o *options) {
		o.KnownFields = true
	}
}

// WithEnv lets an environment variable override the value a key holds in the
// document.
//
// A variable is named after the path its key sits at, prefixed with
// DefaultEnvPrefix, upper-cased, with every separator replaced by an
// underscore: server.endpoint reads YANET_SERVER_ENDPOINT and
// gateways[0].endpoint reads YANET_GATEWAYS_0_ENDPOINT. A list element beyond
// the ones the file defines is appended, so a deployment can add a gateway
// without shipping a different file, and dropping one is a matter of not
// naming it rather than blanking it out.
//
// An override is applied to the document before it is decoded, so the value
// still passes through the destination type's own decoding and validation: an
// empty NonEmptyString is rejected whether it came from the file or the
// environment.
func WithEnv() Option {
	return func(o *options) {
		o.Env = true
	}
}

// WithEnvPrefix overrides the prefix WithEnv expects, for a binary that must
// not read the shared YANET_ namespace.
//
// An empty prefix asks for the unprefixed namespace, where server.endpoint
// reads SERVER_ENDPOINT.
func WithEnvPrefix(prefix string) Option {
	return func(o *options) {
		o.Env = true
		o.EnvPrefix = prefix
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
// It also rejects a key present with a null value, whether or not WithKnownFields is set.
func Decode(buf []byte, dst any, options ...Option) error {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}
	dstType := reflect.TypeOf(dst)
	if dstType == nil {
		return fmt.Errorf("destination must not be nil")
	}

	if opts.Env {
		// Applied before the known-keys walk so an override lands under
		// the same scrutiny as a key written in the file.
		overlaid, err := applyEnv(
			buf,
			dst,
			opts.EnvPrefix,
			environ(opts.EnvPrefix),
		)
		if err != nil {
			return err
		}
		buf = overlaid
	}

	collected, complete, err := walkDocument(
		buf,
		dstType,
		maxAliasExpansionWork,
	)
	if err != nil {
		return err
	}
	if opts.KnownFields {
		if err := unknownKeysError(collected.Unknown); err != nil {
			return err
		}
	}
	if err := nullValuesError(collected.Nulls); err != nil {
		return err
	}

	decoded := false
	if !complete {
		if err := decodeYAML(buf, dst, false); err != nil {
			return err
		}
		decoded = true

		collected, _, err = walkDocument(buf, dstType, 0)
		if err != nil {
			return err
		}
		if opts.KnownFields {
			if err := unknownKeysError(collected.Unknown); err != nil {
				return err
			}
		}
		if err := nullValuesError(collected.Nulls); err != nil {
			return err
		}
	}

	if !decoded {
		if err := decodeYAML(buf, dst, opts.KnownFields); err != nil {
			return err
		}
	}
	return validate(reflect.ValueOf(dst), "")
}

func decodeYAML(buf []byte, dst any, knownFields bool) error {
	if !knownFields {
		return yaml.Unmarshal(buf, dst)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(buf))
	decoder.KnownFields(true)
	if err := decoder.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func validate(v reflect.Value, path string) error {
	// Dereference pointers.
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	if v.CanAddr() {
		if val, ok := v.Addr().Interface().(validatable); ok {
			if err := val.Validate(); err != nil {
				if path == "" {
					return err
				}
				return &PathError{Path: path, Err: err}
			}
		}

		// A wrapper whose wrapped value carries the validation recurses
		// into that value under the same path, rather than being treated
		// as a plain field with no Validate of its own. Checked after
		// validatable so a wrapper implementing both still runs its own
		// Validate.
		if elemer, ok := v.Addr().Interface().(validatableElem); ok {
			ev := elemer.Elem()
			if !ev.IsValid() {
				return nil
			}
			return validate(ev, path)
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
