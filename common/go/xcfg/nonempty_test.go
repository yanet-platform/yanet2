package xcfg

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func Test_NonEmptyString(t *testing.T) {
	v, err := NewNonEmptyString("some")
	require.NoError(t, err)
	require.Equal(t, "some", v.Unwrap())
	require.Equal(t, "some", v.String())
}

func Test_NonEmptyString_NewRejectsEmpty(t *testing.T) {
	_, err := NewNonEmptyString("")
	require.Error(t, err)
}

func Test_NonEmptyString_NewAcceptsWhitespace(t *testing.T) {
	v, err := NewNonEmptyString(" ")
	require.NoError(t, err)
	require.Equal(t, " ", v.Unwrap())
}

func Test_NonEmptyString_MustPanicsOnEmpty(t *testing.T) {
	require.Panics(t, func() { MustNonEmptyString("") })
}

func Test_NonEmptyString_MustSucceeds(t *testing.T) {
	v := MustNonEmptyString("ok")
	require.Equal(t, "ok", v.Unwrap())
}

func Test_NonEmptyString_ValidateRejectsZeroValue(t *testing.T) {
	var zero NonEmptyString
	require.Error(t, zero.Validate())
}

func Test_NonEmptyString_ValidateAcceptsConstructed(t *testing.T) {
	v := MustNonEmptyString("ok")
	require.NoError(t, v.Validate())
}

func Test_NonEmptyString_UnmarshalRejectsEmpty(t *testing.T) {
	var out struct {
		V NonEmptyString `yaml:"v"`
	}
	require.Error(t, yaml.Unmarshal([]byte(`v: ""`), &out))
}

func Test_NonEmptyString_UnmarshalAcceptsValid(t *testing.T) {
	var out struct {
		V NonEmptyString `yaml:"v"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(`v: hello`), &out))
	require.Equal(t, "hello", out.V.Unwrap())
}

func Test_NonEmptyString_NullYAMLCaughtByValidate(t *testing.T) {
	var out struct {
		V NonEmptyString `yaml:"v"`
	}
	// "v:" (null) does not trigger UnmarshalYAML in yaml.v3,
	// leaving zero value.
	//
	// Validate() catches this instead.
	require.NoError(t, yaml.Unmarshal([]byte("v:"), &out))
	require.Error(t, out.V.Validate())
}

func Test_NonEmptyString_YAMLRoundTrip(t *testing.T) {
	type doc struct {
		V NonEmptyString `yaml:"v"`
	}

	original := doc{V: MustNonEmptyString("/dev/hugepages/yanet")}

	buf, err := yaml.Marshal(original)
	require.NoError(t, err)

	var decoded doc
	require.NoError(t, yaml.Unmarshal(buf, &decoded))
	require.Equal(t, original.V.Unwrap(), decoded.V.Unwrap())
}
