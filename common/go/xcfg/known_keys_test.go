package xcfg_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

type knownKeysInner struct {
	Addr string `yaml:"addr"`
}

type knownKeysConfig struct {
	Name  string              `yaml:"name"`
	Inner knownKeysInner      `yaml:"inner"`
	Base  knownKeysInner      `yaml:"base"`
	A     knownKeysInner      `yaml:"a"`
	Extra map[string]string   `yaml:"extra"`
	Nodes []knownKeysInner    `yaml:"nodes"`
	Raw   yaml.Node           `yaml:"raw"`
	Chain xcfg.NonEmptyString `yaml:"chain"`
}

// knownKeysUntagged has one field left without a yaml tag, to exercise
// collectFields' fallback key resolution against yaml.v3's own.
type knownKeysUntagged struct {
	MemoryPath string
}

func Test_CheckKnownKeys_CleanDocument(t *testing.T) {
	input := `
name: foo
inner:
  addr: 1.2.3.4
extra:
  a: b
nodes:
  - addr: 5.6.7.8
raw:
  anything: goes
chain: c
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input)))
}

func Test_CheckKnownKeys_UnknownAtTopLevel(t *testing.T) {
	input := `
name: foo
bogus: true
`
	err := xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus")
}

func Test_CheckKnownKeys_UnknownNested(t *testing.T) {
	input := `
name: foo
inner:
  addr: 1.2.3.4
  bogus: true
`
	err := xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "inner.bogus")
}

func Test_CheckKnownKeys_ScalarAliasIntoStructFieldNotFlagged(t *testing.T) {
	input := `
name: &n foo
inner: *n
`
	// The inner value decodes from a scalar alias into a struct type, which is a
	// shape mismatch and must be skipped rather than flagged.
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input)))
}

func Test_CheckKnownKeys_UnknownKeyBehindAlias(t *testing.T) {
	input := `
base: &b
  addr: 1.2.3.4
  bogus: true
a: *b
`
	err := xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "base.bogus")
	require.Contains(t, err.Error(), "a.bogus")
}

func Test_CheckKnownKeys_MapValueNotFlagged(t *testing.T) {
	input := `
name: foo
extra:
  anything_at_all: value
  another_free_key: value
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input)))
}

func Test_CheckKnownKeys_YAMLNodeFieldNotFlagged(t *testing.T) {
	input := `
name: foo
raw:
  whatever_shape: it_wants
  nested:
    also_free: true
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input)))
}

func Test_CheckKnownKeys_SliceElement(t *testing.T) {
	input := `
name: foo
nodes:
  - addr: 1.2.3.4
  - addr: 5.6.7.8
    bogus: true
`
	err := xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "nodes[1].bogus")
}

func Test_CheckKnownKeys_ScalarUnmarshalerNotFlagged(t *testing.T) {
	// The chain value is xcfg.NonEmptyString, which decodes from a bare scalar.
	// No keys need checking underneath it.
	input := `
name: foo
chain: some-value
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input)))
}

func Test_CheckKnownKeys_MultipleUnknownKeysReportedTogether(t *testing.T) {
	input := `
name: foo
bogus_one: true
inner:
  addr: 1.2.3.4
  bogus_two: true
`
	err := xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus_one")
	require.Contains(t, err.Error(), "inner.bogus_two")
}

func Test_CheckKnownKeys_MergeKeySingleAliasNotFlagged(t *testing.T) {
	input := `
base: &b
  addr: 1.2.3.4
inner:
  <<: *b
name: x
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input)))
}

func Test_CheckKnownKeys_MergeKeySequenceAliasNotFlagged(t *testing.T) {
	input := `
base: &b
  addr: 1.2.3.4
a: &c
  addr: 5.6.7.8
inner:
  <<: [*b, *c]
name: x
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input)))
}

func Test_CheckKnownKeys_MergeKeyUnknownInMergedMappingReported(t *testing.T) {
	input := `
base: &b
  addr: 1.2.3.4
  bogus: true
inner:
  <<: *b
name: x
`
	err := xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "base.bogus")
	require.Contains(t, err.Error(), "inner.bogus")
}

func Test_CheckKnownKeys_SelfReferentialMergeAnchorTerminates(t *testing.T) {
	// A merge key whose alias resolves back to its own mapping node is a
	// cycle: yaml.v3 rejects it while decoding into a Go value but not
	// while decoding into a node tree, so CheckKnownKeys must bound the
	// recursion itself instead of overflowing the stack.
	input := `
base: &b
  addr: 1.2.3.4
  <<: *b
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input)))
}

func Test_CheckKnownKeys_SelfReferentialMergeSequenceTerminates(t *testing.T) {
	input := `
base: &b
  addr: 1.2.3.4
  <<: [*b]
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input)))
}

func Test_CheckKnownKeys_MergeKeySiblingsBothWalked(t *testing.T) {
	// The same anchor merged into two independent mappings is not a cycle,
	// so an unknown key inside it must be reported at both merge sites,
	// proving the cycle guard is scoped to a descent path rather than
	// shared globally across the whole walk.
	input := `
base: &b
  addr: 1.2.3.4
  bogus: true
inner:
  <<: *b
a:
  <<: *b
name: x
`
	err := xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "base.bogus")
	require.Contains(t, err.Error(), "inner.bogus")
	require.Contains(t, err.Error(), "a.bogus")
}

func Test_CheckKnownKeys_UntaggedFieldLowercasedNameNotFlagged(t *testing.T) {
	// The yaml.v3 package decodes an untagged field under its lowercased Go name.
	input := `
memorypath: /dev/hugepages/yanet
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysUntagged]([]byte(input)))
}

func Test_CheckKnownKeys_UntaggedFieldOriginalCaseFlagged(t *testing.T) {
	// The yaml.v3 package silently drops this key rather than decoding it into
	// MemoryPath, so it must be reported rather than accepted.
	input := `
MemoryPath: /dev/hugepages/yanet
`
	err := xcfg.CheckKnownKeys[knownKeysUntagged]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "MemoryPath")
}

// knownKeysInlineMap has an inline map field that catches any key not
// matched by Name, mirroring yaml.v3's own ",inline" decoding.
type knownKeysInlineMap struct {
	Name  string            `yaml:"name"`
	Extra map[string]string `yaml:",inline"`
}

func Test_CheckKnownKeys_InlineMapCatchAllNotFlagged(t *testing.T) {
	input := `
name: foo
anything_at_all: value
another_free_key: value
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysInlineMap]([]byte(input)))
}

type requiredWrappedInner struct {
	A string `yaml:"a"`
	B string `yaml:"b"`
}

type requiredWrapped struct {
	Sub xcfg.Required[requiredWrappedInner] `yaml:"sub"`
}

func Test_CheckKnownKeys_UnexportedStructWithUnmarshalYAMLNotFlagged(t *testing.T) {
	// Required[T] has only unexported fields but decodes a mapping through
	// its own UnmarshalYAML — the walk must not report the mapping's keys
	// as unknown just because Required[T] has no exported field set of its
	// own.
	input := "sub:\n  a: x\n  b: y\n"
	require.NoError(t, xcfg.CheckKnownKeys[requiredWrapped]([]byte(input)))

	var out requiredWrapped
	require.NoError(t, yaml.Unmarshal([]byte(input), &out))
	require.Equal(t, "x", out.Sub.Unwrap().A)
	require.Equal(t, "y", out.Sub.Unwrap().B)
}

type optionalWrapped struct {
	Sub xcfg.Optional[requiredWrappedInner] `yaml:"sub"`
}

// Test_CheckKnownKeys_OptionalSeesThroughToWrappedFields asserts that a
// stray key nested inside an Optional[T] block is still reported.
//
// Optional[T] has the same unexported-field, UnmarshalYAML shape as
// Required[T], which the previous test proves is otherwise treated as
// opaque to the walk. Without Optional's WalkType hook this key would be
// silently accepted instead, undoing WithKnownFields for every optional
// module or device block.
func Test_CheckKnownKeys_OptionalSeesThroughToWrappedFields(t *testing.T) {
	input := "sub:\n  a: x\n  b: y\n  bogus: z\n"
	err := xcfg.CheckKnownKeys[optionalWrapped]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "sub.bogus")
}

// Test_CheckKnownKeys_OptionalCleanDocumentNotFlagged is the positive
// control for Test_CheckKnownKeys_OptionalSeesThroughToWrappedFields,
// proving the hook does not over-report on a document with no stray keys.
func Test_CheckKnownKeys_OptionalCleanDocumentNotFlagged(t *testing.T) {
	input := "sub:\n  a: x\n  b: y\n"
	require.NoError(t, xcfg.CheckKnownKeys[optionalWrapped]([]byte(input)))
}

// optionalAliasModules mirrors bundle.ModulesConfig: a struct whose fields
// are Optional[T]-wrapped module configs keyed by module name.
type optionalAliasModules struct {
	Decap xcfg.Optional[requiredWrappedInner] `yaml:"decap"`
}

type optionalAliasConfig struct {
	Name    string               `yaml:"name"`
	Modules optionalAliasModules `yaml:"modules"`
}

// Test_CheckKnownKeys_AliasedMapKeyStillChecked is a regression test for an
// alias used as a YAML mapping key, mirroring yncp.Config's modules block.
//
// An alias key node's Value holds its anchor name rather than the value it
// resolves to, so the fixture names the anchor after the field it targets
// (decap) for the walk to match it against Decap's struct field.
func Test_CheckKnownKeys_AliasedMapKeyStillChecked(t *testing.T) {
	input := `
name: &decap decap
modules:
  *decap:
    a: x
    b: y
    bogus: z
`
	err := xcfg.CheckKnownKeys[optionalAliasConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "modules.decap.bogus")
}

// mapAliasConfig mirrors optionalAliasConfig but keys its module map by a
// map[string]... field instead of a struct field, so an alias used as its
// key exercises the reflect.Map arm of walkMappingNode instead of the
// struct arm Test_CheckKnownKeys_AliasedMapKeyStillChecked exercises.
type mapAliasConfig struct {
	Name    string                                         `yaml:"name"`
	Modules map[string]xcfg.Optional[requiredWrappedInner] `yaml:"modules"`
}

// Test_CheckKnownKeys_AliasedMapKeyInMapField is the map-branch counterpart
// to Test_CheckKnownKeys_AliasedMapKeyStillChecked.
//
// A map key accepts any name, so the alias's anchor name lands directly as
// the map key rather than needing to match a struct field tag.
func Test_CheckKnownKeys_AliasedMapKeyInMapField(t *testing.T) {
	input := `
name: &n foo
modules:
  *n:
    a: x
    b: y
    bogus: z
`
	err := xcfg.CheckKnownKeys[mapAliasConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "modules.n.bogus")
}

// Test_CheckKnownKeys_NullKeyStillReported pins that a YAML null key (a
// bare "?" with no scalar value) is still reported as an unknown key named
// "", rather than silently skipped by the complex-key guard alongside
// sequence and mapping keys.
//
// yaml.v3 drops a null key silently during a real decode, so this walk is
// the only place left to surface it at all.
func Test_CheckKnownKeys_NullKeyStillReported(t *testing.T) {
	input := "? \n: 1\n"
	err := xcfg.CheckKnownKeys[knownKeysConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown key "" specified at line 1`)
}

// Test_Decode_ComplexMappingKey_RejectedNotMisreported asserts that a YAML
// complex key (a sequence or mapping used as a mapping key) is neither
// reported as an unknown key named "" nor silently accepted.
//
// yaml.v3's mappingStruct decodes a struct's key into a string
// unconditionally, so a complex key can never name a struct field.
// walkMappingNode's struct branch skips it instead of reporting it as
// unknown, and yaml.v3's own strict decode still rejects the document
// because the key cannot unmarshal into that string.
func Test_Decode_ComplexMappingKey_RejectedNotMisreported(t *testing.T) {
	input := "? [a, b]\n: 1\n"

	var cfg knownKeysConfig
	err := xcfg.Decode([]byte(input), &cfg, xcfg.WithKnownFields())
	require.Error(t, err)
	require.NotContains(t, err.Error(), `unknown key ""`)
	require.Contains(t, err.Error(), "cannot unmarshal !!seq into string")

	mappingKeyInput := "? {a: 1}\n: 2\n"

	var mappingKeyCfg knownKeysConfig
	mappingKeyErr := xcfg.Decode([]byte(mappingKeyInput), &mappingKeyCfg, xcfg.WithKnownFields())
	require.Error(t, mappingKeyErr)
	require.NotContains(t, mappingKeyErr.Error(), `unknown key ""`)
	require.Contains(t, mappingKeyErr.Error(), "cannot unmarshal !!map into string")
}

// Test_Decode_MalformedDocument_SameErrorBothModes asserts that a document
// yaml.v3 itself cannot parse reports the identical error whether or not
// WithKnownFields is set, since CheckKnownKeys must not add a wrapping
// clause the plain decode path lacks.
func Test_Decode_MalformedDocument_SameErrorBothModes(t *testing.T) {
	input := "name: foo\n  bogus: bar\n"

	var strict knownKeysConfig
	errStrict := xcfg.Decode([]byte(input), &strict, xcfg.WithKnownFields())

	var lenient knownKeysConfig
	errLenient := xcfg.Decode([]byte(input), &lenient)

	require.Error(t, errStrict)
	require.Error(t, errLenient)
	require.Equal(t, errLenient.Error(), errStrict.Error())
}

type complexMapKeyInner struct {
	A string `yaml:"a"`
}

type complexMapKeyConfig struct {
	Modules map[[2]string]xcfg.Optional[complexMapKeyInner] `yaml:"modules"`
}

// Test_Decode_ComplexMapKeyUnknownFieldReported pins that an unknown field
// nested under a complex map key is still reported.
//
// A [2]string map key decodes a YAML sequence key with no error from
// yaml.v3, unlike a struct key or a string-keyed map, so nothing outside
// this walk ever rejects the document. Skipping a complex key in the map
// branch the way the struct branch does would let the unknown field escape
// unreported and the whole document decode silently.
func Test_Decode_ComplexMapKeyUnknownFieldReported(t *testing.T) {
	input := `
modules:
  ? [x, y]
  : a: v
    bogus: z
`
	err := xcfg.Decode([]byte(input), new(complexMapKeyConfig), xcfg.WithKnownFields())
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus")
}
