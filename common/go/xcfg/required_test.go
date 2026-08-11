package xcfg_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Test_Required_UnmarshalAcceptsExplicitValue asserts that an explicit value
// decodes and validates successfully.
func Test_Required_UnmarshalAcceptsExplicitValue(t *testing.T) {
	var out struct {
		V xcfg.Required[uint32] `yaml:"v"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("v: 3"), &out))
	require.NoError(t, out.V.Validate())
	require.Equal(t, uint32(3), out.V.Unwrap())
}

// Test_Required_UnmarshalAcceptsExplicitZero asserts that an explicit zero
// value is accepted, unlike NonZero.
func Test_Required_UnmarshalAcceptsExplicitZero(t *testing.T) {
	var out struct {
		V xcfg.Required[uint32] `yaml:"v"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("v: 0"), &out))
	require.NoError(t, out.V.Validate())
	require.Equal(t, uint32(0), out.V.Unwrap())
}

// Test_Required_ValidateRejectsAbsentKey asserts that a document that omits
// the key entirely fails validation.
func Test_Required_ValidateRejectsAbsentKey(t *testing.T) {
	var out struct {
		V xcfg.Required[uint32] `yaml:"v"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("{}"), &out))
	require.Error(t, out.V.Validate())
}

// Test_Required_ValidateRejectsNullValue asserts that a YAML null value,
// which yaml.v3 does not route through UnmarshalYAML, also fails validation.
func Test_Required_ValidateRejectsNullValue(t *testing.T) {
	var out struct {
		V xcfg.Required[uint32] `yaml:"v"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("v:"), &out))
	require.Error(t, out.V.Validate())
}

// Test_Required_UnmarshalRejectsMalformedScalar asserts that a value that
// cannot decode into the target type fails to unmarshal.
func Test_Required_UnmarshalRejectsMalformedScalar(t *testing.T) {
	var out struct {
		V xcfg.Required[uint32] `yaml:"v"`
	}
	require.Error(t, yaml.Unmarshal([]byte("v: not-a-number"), &out))
}

// Test_Required_YAMLRoundTrip asserts that a marshaled Required decodes back
// to the same explicit value.
func Test_Required_YAMLRoundTrip(t *testing.T) {
	type doc struct {
		V xcfg.Required[uint32] `yaml:"v"`
	}

	original := doc{V: xcfg.NewRequired(uint32(5))}

	buf, err := yaml.Marshal(original)
	require.NoError(t, err)

	var decoded doc
	require.NoError(t, yaml.Unmarshal(buf, &decoded))
	require.Equal(t, original.V.Unwrap(), decoded.V.Unwrap())
	require.NoError(t, decoded.V.Validate())
}

// Test_Required_LoadRejectsMissing asserts that xcfg.Decode surfaces a
// dotted path naming the missing field.
func Test_Required_LoadRejectsMissing(t *testing.T) {
	type cfg struct {
		Count xcfg.Required[int] `yaml:"count"`
	}

	var out cfg
	err := xcfg.Decode([]byte("{}"), &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "count")
}
