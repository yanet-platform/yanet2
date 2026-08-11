package xcfg_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

type optionalInner struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

func (m *optionalInner) Default() {
	*m = optionalInner{Port: 8080}
}

// Test_Optional_AbsentKeyUnwrapsNil asserts that a document omitting the key
// entirely leaves Unwrap returning nil, distinguishing absence from a
// present zero value.
func Test_Optional_AbsentKeyUnwrapsNil(t *testing.T) {
	var out struct {
		V xcfg.Optional[optionalInner] `yaml:"v"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("{}"), &out))
	require.Nil(t, out.V.Unwrap())
}

// Test_Optional_PresentKeySeedsDefaultsBeforeDecode asserts that a present
// key first seeds the wrapped value with its Default before decoding, so an
// omitted field within it keeps that default rather than a zero value.
func Test_Optional_PresentKeySeedsDefaultsBeforeDecode(t *testing.T) {
	var out struct {
		V xcfg.Optional[optionalInner] `yaml:"v"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("v:\n  name: foo\n"), &out))
	require.NotNil(t, out.V.Unwrap())
	require.Equal(t, "foo", out.V.Unwrap().Name)
	require.Equal(t, 8080, out.V.Unwrap().Port)
}

// Test_Optional_NewOptionalRoundTrip asserts that a value built with
// NewOptional decodes back to the same value through YAML.
func Test_Optional_NewOptionalRoundTrip(t *testing.T) {
	type doc struct {
		V xcfg.Optional[optionalInner] `yaml:"v"`
	}

	original := doc{V: xcfg.NewOptional(optionalInner{Name: "bar", Port: 1})}

	buf, err := yaml.Marshal(original)
	require.NoError(t, err)

	var decoded doc
	require.NoError(t, yaml.Unmarshal(buf, &decoded))
	require.Equal(t, original.V.Unwrap(), decoded.V.Unwrap())
}

// Test_Optional_JSONDistinguishesAbsentFromPresent asserts that
// MarshalJSON, which zap.Any relies on to log a config, renders an absent
// Optional as null and a present one as the value it wraps, rather than
// collapsing both to "{}" through the unexported field.
func Test_Optional_JSONDistinguishesAbsentFromPresent(t *testing.T) {
	var absent struct {
		V xcfg.Optional[optionalInner] `yaml:"v"`
	}
	require.NoError(t, yaml.Unmarshal([]byte("{}"), &absent))
	buf, err := json.Marshal(absent.V)
	require.NoError(t, err)
	require.JSONEq(t, "null", string(buf))

	present := xcfg.NewOptional(optionalInner{Name: "foo", Port: 1})
	buf, err = json.Marshal(present)
	require.NoError(t, err)
	require.JSONEq(t, `{"Name":"foo","Port":1}`, string(buf))
}

type optionalWithRequired struct {
	InstanceID xcfg.Required[uint32] `yaml:"instance_id"`
}

// Test_Optional_DecodeValidatesWrappedField asserts that xcfg.Decode
// recurses into a present Optional's own fields and reports a dotted path
// rooted at the wrapper, proving validation is not swallowed by Optional's
// unexported field the way an unrelated struct field would be.
func Test_Optional_DecodeValidatesWrappedField(t *testing.T) {
	type doc struct {
		Module xcfg.Optional[optionalWithRequired] `yaml:"module"`
	}

	var out doc
	err := xcfg.Decode([]byte("module: {}\n"), &out)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module.instance_id")
}

// Test_Optional_DecodeAbsentSkipsValidation asserts that xcfg.Decode raises
// no error for a document that omits the Optional entirely, even though its
// wrapped type has a Required field.
func Test_Optional_DecodeAbsentSkipsValidation(t *testing.T) {
	type doc struct {
		Module xcfg.Optional[optionalWithRequired] `yaml:"module"`
	}

	var out doc
	require.NoError(t, xcfg.Decode([]byte("{}\n"), &out))
}
