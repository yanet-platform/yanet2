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
