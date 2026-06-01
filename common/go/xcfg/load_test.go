package xcfg

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Load_Valid(t *testing.T) {
	type Config struct {
		Name NonEmptyString `yaml:"name"`
		Path NonEmptyString `yaml:"path"`
	}

	var cfg Config
	require.NoError(t, Decode([]byte("name: foo\npath: /tmp"), &cfg))
	require.Equal(t, "foo", cfg.Name.Unwrap())
	require.Equal(t, "/tmp", cfg.Path.Unwrap())
}

func Test_Load_RejectsEmptyString(t *testing.T) {
	type Config struct {
		Name NonEmptyString `yaml:"name"`
	}

	var cfg Config
	require.Error(t, Decode([]byte(`name: ""`), &cfg))
}

func Test_Load_RejectsNullField(t *testing.T) {
	type Config struct {
		Name NonEmptyString `yaml:"name"`
		Path NonEmptyString `yaml:"path"`
	}

	var cfg Config
	err := Decode([]byte("name:\npath: /tmp"), &cfg)

	var pathErr *PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, "name", pathErr.Path)
}

func Test_Load_RejectsMissingField(t *testing.T) {
	type Config struct {
		Name NonEmptyString `yaml:"name"`
		Path NonEmptyString `yaml:"path"`
	}

	var cfg Config
	err := Decode([]byte("path: /tmp"), &cfg)

	var pathErr *PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, "name", pathErr.Path)
}

func Test_Load_ValidatesNestedStruct(t *testing.T) {
	type Inner struct {
		Addr NonEmptyString `yaml:"addr"`
	}
	type Outer struct {
		Name  NonEmptyString `yaml:"name"`
		Inner Inner          `yaml:"inner"`
	}

	var cfg Outer
	require.NoError(t, Decode([]byte("name: x\ninner:\n  addr: y"), &cfg))
}

func Test_Load_RejectsNestedNull(t *testing.T) {
	type Inner struct {
		Addr NonEmptyString `yaml:"addr"`
	}
	type Outer struct {
		Name  NonEmptyString `yaml:"name"`
		Inner Inner          `yaml:"inner"`
	}

	var cfg Outer
	err := Decode([]byte("name: x\ninner:\n  addr:"), &cfg)

	var pathErr *PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, "inner.addr", pathErr.Path)
}

func Test_Load_SkipsNilPointer(t *testing.T) {
	type Inner struct {
		Path NonEmptyString `yaml:"path"`
	}
	type Outer struct {
		Inner *Inner `yaml:"inner"`
	}

	var cfg Outer
	require.NoError(t, Decode([]byte("{}"), &cfg))
}

func Test_Load_ValidatesNonNilPointer(t *testing.T) {
	type Inner struct {
		Path NonEmptyString `yaml:"path"`
	}
	type Outer struct {
		Inner *Inner `yaml:"inner"`
	}

	var cfg Outer
	require.Error(t, Decode([]byte("inner:\n  path:"), &cfg))
}

func Test_Load_PreservesDefaults(t *testing.T) {
	type Config struct {
		Name NonEmptyString `yaml:"name"`
		Path NonEmptyString `yaml:"path"`
	}

	cfg := Config{
		Name: MustNonEmptyString("default-name"),
		Path: MustNonEmptyString("/default/path"),
	}
	require.NoError(t, Decode([]byte("name: custom"), &cfg))
	require.Equal(t, "custom", cfg.Name.Unwrap())
	require.Equal(t, "/default/path", cfg.Path.Unwrap())
}

func Test_Load_SliceElement_MissingField(t *testing.T) {
	type Item struct {
		Name NonEmptyString `yaml:"name"`
	}
	type Config struct {
		Items []Item `yaml:"items"`
	}

	// The first element omits "name" entirely — zero value NonEmptyString must
	// be caught by the walker.
	yaml := "items:\n  - {}\n"
	var cfg Config
	err := Decode([]byte(yaml), &cfg)

	var pathErr *PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, "items[0].name", pathErr.Path)
}

func Test_Load_SliceElement_ExplicitEmptyField(t *testing.T) {
	type Item struct {
		Name NonEmptyString `yaml:"name"`
	}
	type Config struct {
		Items []Item `yaml:"items"`
	}

	// An explicit empty string is rejected at UnmarshalYAML; Decode must still
	// surface an error.
	yaml := "items:\n  - name: \"\"\n"
	var cfg Config
	err := Decode([]byte(yaml), &cfg)
	require.Error(t, err)
}

func Test_Load_SliceElement_Valid(t *testing.T) {
	type Item struct {
		Name NonEmptyString `yaml:"name"`
	}
	type Config struct {
		Items []Item `yaml:"items"`
	}

	yaml := "items:\n  - name: alpha\n  - name: beta\n"
	var cfg Config
	require.NoError(t, Decode([]byte(yaml), &cfg))
	require.Equal(t, "alpha", cfg.Items[0].Name.Unwrap())
	require.Equal(t, "beta", cfg.Items[1].Name.Unwrap())
}

func Test_Load_SliceElement_SecondElementMissingField(t *testing.T) {
	type Item struct {
		Name  NonEmptyString `yaml:"name"`
		Value NonEmptyString `yaml:"value"`
	}
	type Config struct {
		Items []Item `yaml:"items"`
	}

	// The second element omits "value" — the walker must catch it.
	yaml := "items:\n  - name: alpha\n    value: v1\n  - name: beta\n"
	var cfg Config
	err := Decode([]byte(yaml), &cfg)

	var pathErr *PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, "items[1].value", pathErr.Path)
}

func Test_Load_LineErrorUnwrapsFromPathError(t *testing.T) {
	// Verify that LineError from UnmarshalYAML is accessible via
	// errors.As through the error chain, even when Decode doesn't
	// directly produce it (yaml.v3 wraps it in TypeError).
	var lineErr *LineError
	err := errors.New("not a line error")
	require.False(t, errors.As(err, &lineErr))

	le := &LineError{Line: 5, Err: errors.New("test")}
	pe := &PathError{Path: "a.b", Err: le}
	require.True(t, errors.As(pe, &lineErr))
	require.Equal(t, 5, lineErr.Line)
}
