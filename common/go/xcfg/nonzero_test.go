package xcfg

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func Test_NonZero(t *testing.T) {
	v := MustNonZero(7)
	require.Equal(t, 7, v.Unwrap())
}

func Test_NonZero_NewRejectsZero(t *testing.T) {
	_, err := NewNonZero(0)
	require.Error(t, err)
}

func Test_NonZero_NewAcceptsPositive(t *testing.T) {
	v, err := NewNonZero(42)
	require.NoError(t, err)
	require.Equal(t, 42, v.Unwrap())
	require.Equal(t, "42", v.String())
}

func Test_NonZero_NewAcceptsNegative(t *testing.T) {
	v, err := NewNonZero(-1)
	require.NoError(t, err)
	require.Equal(t, -1, v.Unwrap())
}

func Test_NonZero_MustPanicsOnZero(t *testing.T) {
	require.Panics(t, func() { MustNonZero(0) })
}

func Test_NonZero_ValidateRejectsZeroValue(t *testing.T) {
	var zero NonZero[int]
	require.Error(t, zero.Validate())
}

func Test_NonZero_ValidateAcceptsConstructed(t *testing.T) {
	v := MustNonZero(5)
	require.NoError(t, v.Validate())
}

func Test_NonZero_UnmarshalRejectsZero(t *testing.T) {
	var out struct {
		V NonZero[int] `yaml:"v"`
	}
	require.Error(t, yaml.Unmarshal([]byte("v: 0"), &out))
}

func Test_NonZero_UnmarshalAcceptsPositive(t *testing.T) {
	var out struct {
		V NonZero[int] `yaml:"v"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("v: 10"), &out))
	require.Equal(t, 10, out.V.Unwrap())
}

func Test_NonZero_NullYAMLCaughtByValidate(t *testing.T) {
	var out struct {
		V NonZero[int] `yaml:"v"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("v:"), &out))
	require.Error(t, out.V.Validate())
}

func Test_NonZero_YAMLRoundTrip(t *testing.T) {
	type doc struct {
		V NonZero[int] `yaml:"v"`
	}

	original := doc{V: MustNonZero(64)}

	buf, err := yaml.Marshal(original)
	require.NoError(t, err)

	var decoded doc
	require.NoError(t, yaml.Unmarshal(buf, &decoded))
	require.Equal(t, original.V.Unwrap(), decoded.V.Unwrap())
}

func Test_NonZero_WorksWithTypeAlias(t *testing.T) {
	// NonZero works with custom numeric types.
	type mySize uint64

	v, err := NewNonZero(mySize(128))
	require.NoError(t, err)
	require.Equal(t, mySize(128), v.Unwrap())
}

func Test_NonZero_WorksWithLoad(t *testing.T) {
	type Config struct {
		Name  NonEmptyString `yaml:"name"`
		Count NonZero[int]   `yaml:"count"`
	}

	var cfg Config
	require.NoError(t, Decode([]byte("name: foo\ncount: 5"), &cfg))
	require.Equal(t, 5, cfg.Count.Unwrap())
}

func Test_NonZero_LoadRejectsMissing(t *testing.T) {
	type Config struct {
		Count NonZero[int] `yaml:"count"`
	}

	var cfg Config
	err := Decode([]byte("{}"), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "count")
}
