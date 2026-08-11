package xcfg

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

	// An explicit empty string is rejected at UnmarshalYAML. Decode must still
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

func Test_Decode_UnknownField_IgnoredByDefault(t *testing.T) {
	// By default an unknown key is silently dropped, leaving the rest of the
	// document to decode normally. This documents the current lenient
	// behaviour that WithKnownFields is meant to opt out of.
	type Config struct {
		Name NonEmptyString `yaml:"name"`
	}

	var cfg Config
	err := Decode([]byte("name: foo\nunknown_key: bar"), &cfg)
	require.NoError(t, err)
	require.Equal(t, "foo", cfg.Name.Unwrap())
}

func Test_Decode_UnknownField_RejectedWithKnownFields(t *testing.T) {
	// With WithKnownFields, an unknown key must fail loudly instead of being
	// silently dropped, and the error must name the offending key.
	type Config struct {
		Name NonEmptyString `yaml:"name"`
	}

	var cfg Config
	err := Decode([]byte("name: foo\nunknown_key: bar"), &cfg, WithKnownFields())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown_key")
}

func Test_Decode_EmptyDocument_NoErrorInEitherMode(t *testing.T) {
	// yaml.Decoder.Decode returns io.EOF on an empty document, unlike
	// yaml.Unmarshal which treats it as a no-op. Both modes must preserve
	// the lenient behaviour: no error, and defaults left untouched.
	type Config struct {
		Name NonEmptyString `yaml:"name"`
	}

	testCases := []struct {
		name    string
		options []Option
	}{
		{
			name:    "lenient",
			options: nil,
		},
		{
			name:    "known fields",
			options: []Option{WithKnownFields()},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := Config{Name: MustNonEmptyString("default-name")}
			err := Decode([]byte(""), &cfg, testCase.options...)
			require.NoError(t, err)
			require.Equal(t, "default-name", cfg.Name.Unwrap())
		})
	}
}

func Test_Decode_KnownFields_StillValidates(t *testing.T) {
	// Default()/Validate() behaviour must hold unchanged under
	// WithKnownFields: valid input decodes cleanly, invalid input is still
	// rejected by the field's Validate().
	type Config struct {
		Name NonEmptyString `yaml:"name"`
		Path NonEmptyString `yaml:"path"`
	}

	var validConfig Config
	require.NoError(t, Decode([]byte("name: foo\npath: /tmp"), &validConfig, WithKnownFields()))
	require.Equal(t, "foo", validConfig.Name.Unwrap())
	require.Equal(t, "/tmp", validConfig.Path.Unwrap())

	var invalidConfig Config
	err := Decode([]byte("name: foo"), &invalidConfig, WithKnownFields())

	var pathErr *PathError
	require.ErrorAs(t, err, &pathErr)
	require.Equal(t, "path", pathErr.Path)
}

// customUnmarshalInner mirrors a module Config's UnmarshalYAML: it
// re-decodes through a plain alias, which is the shape yaml.v3's own
// KnownFields decoder cannot see through.
type customUnmarshalInner struct {
	Addr NonEmptyString `yaml:"addr"`
}

func (m *customUnmarshalInner) UnmarshalYAML(node *yaml.Node) error {
	type plain customUnmarshalInner
	return node.Decode((*plain)(m))
}

func Test_Decode_KnownFields_ReachesCustomUnmarshalYAML(t *testing.T) {
	// An unknown key nested under a field whose type implements
	// UnmarshalYAML via node.Decode must still be caught with
	// WithKnownFields, and must still be silently ignored without it.
	type Config struct {
		Name  NonEmptyString       `yaml:"name"`
		Inner customUnmarshalInner `yaml:"inner"`
	}

	input := []byte("name: foo\ninner:\n  addr: bar\n  bogus: true\n")

	var lenient Config
	require.NoError(t, Decode(input, &lenient))

	var strict Config
	err := Decode(input, &strict, WithKnownFields())
	require.Error(t, err)
	require.Contains(t, err.Error(), "inner.bogus")
}

func Test_LoadConfig_KnownFields_ReachesCustomUnmarshalYAML(t *testing.T) {
	// LoadConfig delegates to Decode, so the same reflection-walk coverage
	// must hold when driven through a file path rather than an in-memory
	// buffer.
	type Config struct {
		Name  NonEmptyString       `yaml:"name"`
		Inner customUnmarshalInner `yaml:"inner"`
	}

	path := t.TempDir() + "/config.yaml"
	require.NoError(t, os.WriteFile(path, []byte("name: foo\ninner:\n  addr: bar\n  bogus: true\n"), 0o600))

	_, err := LoadConfig[Config](path)
	require.NoError(t, err)

	_, err = LoadConfig[Config](path, WithKnownFields())
	require.Error(t, err)
	require.Contains(t, err.Error(), "inner.bogus")
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
