package xcfg

import (
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

type validatable interface {
	Validate() error
}

// Decode deserializes YAML data into dst and then recursively validates all
// fields that implement "validatable".
func Decode(buf []byte, dst any) error {
	if err := yaml.Unmarshal(buf, dst); err != nil {
		return err
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
