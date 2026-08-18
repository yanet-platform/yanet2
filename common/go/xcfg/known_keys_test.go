package xcfg_test

import (
	"net/netip"
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
func Test_CheckKnownKeys_AliasedMapKeyStillChecked(t *testing.T) {
	input := `
name: &anchor decap
modules:
  *anchor:
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
// A map key accepts any name, so the alias resolves to the scalar it points
// at rather than needing to match a struct field tag.
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
	require.Contains(t, err.Error(), "modules.foo.bogus")
}

type aliasedKeyNullConfig struct {
	Struct knownKeysInner       `yaml:"struct"`
	Extra  map[string]yaml.Node `yaml:",inline"`
}

// Test_Decode_AliasedKeyNullValueReportedAtResolvedFieldPath asserts a null under an aliased key is reported at the field the alias resolves to.
func Test_Decode_AliasedKeyNullValueReportedAtResolvedFieldPath(t *testing.T) {
	input := "key: &key struct\n*key:\n"
	var cfg aliasedKeyNullConfig
	err := xcfg.Decode([]byte(input), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), `"struct"`)
	require.Contains(t, err.Error(), "has no value")
}

// Test_Decode_AliasedKeyKnownFields_NoUnknownKeyError asserts the same aliased-key document produces no unknown-key error under WithKnownFields.
func Test_Decode_AliasedKeyKnownFields_NoUnknownKeyError(t *testing.T) {
	input := "key: &key struct\n*key:\n"
	err := xcfg.Decode([]byte(input), new(aliasedKeyNullConfig), xcfg.WithKnownFields())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "unknown key")
}

// Test_Decode_DuplicateResolvedKey_BodyLastAccepted asserts a plain key and an aliased key resolving to the same entry: the last one, a body, wins and is accepted.
func Test_Decode_DuplicateResolvedKey_BodyLastAccepted(t *testing.T) {
	input := "tag: &anchor a\nmodules:\n  a:\n  *anchor:\n    addr: real\n"
	var cfg mapMergeConfig
	err := xcfg.Decode([]byte(input), &cfg)
	require.NoError(t, err)
}

// Test_Decode_DuplicateResolvedKey_NullLastReported mirrors Test_Decode_DuplicateResolvedKey_BodyLastAccepted: the last occurrence, a null, wins and is reported.
func Test_Decode_DuplicateResolvedKey_NullLastReported(t *testing.T) {
	input := "tag: &anchor a\nmodules:\n  *anchor:\n    addr: real\n  a:\n"
	var cfg mapMergeConfig
	err := xcfg.Decode([]byte(input), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), `"modules.a"`)
	require.Contains(t, err.Error(), "has no value")
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

// Test_Decode_MergedComplexKeyNullReported pins that a merged entry under a complex map key is walked like a direct one, so its null is still reported.
func Test_Decode_MergedComplexKeyNullReported(t *testing.T) {
	input := `
base: &b
  ? [x, y]
  :
modules:
  <<: *b
`
	var cfg complexMapKeyConfig
	err := xcfg.Decode([]byte(input), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "has no value")
}

// Test_Decode_NullMergeValue_YAMLNativeErrorSurfaces asserts that a null merge key value is not flagged null, leaving yaml.v3's own merge error.
func Test_Decode_NullMergeValue_YAMLNativeErrorSurfaces(t *testing.T) {
	var cfg knownKeysConfig
	err := xcfg.Decode([]byte("inner:\n  <<:\n  addr: x\n"), &cfg)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "has no value")
	require.Contains(t, err.Error(), "map merge requires map or sequence of maps as the value")
}

// Test_Decode_NullMergeValueAtRoot_YAMLNativeErrorSurfaces is the document-root counterpart of the null merge value test.
func Test_Decode_NullMergeValueAtRoot_YAMLNativeErrorSurfaces(t *testing.T) {
	var cfg knownKeysConfig
	err := xcfg.Decode([]byte("<<:\nname: x\n"), &cfg)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "has no value")
	require.Contains(t, err.Error(), "map merge requires map or sequence of maps as the value")
}

// Test_Decode_NullMergeValueSequenceElement_YAMLNativeErrorSurfaces is the sequence-element counterpart of the null merge value test.
func Test_Decode_NullMergeValueSequenceElement_YAMLNativeErrorSurfaces(t *testing.T) {
	var cfg knownKeysConfig
	err := xcfg.Decode([]byte("inner:\n  <<: [~]\n  addr: x\n"), &cfg)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "has no value")
	require.Contains(t, err.Error(), "map merge requires map or sequence of maps as the value")
}

type mapMergeConfig struct {
	Modules map[string]knownKeysInner `yaml:"modules"`
	Extra   map[string]yaml.Node      `yaml:",inline"`
}

// Test_Decode_NullMergeValueMapField_YAMLNativeErrorSurfaces is the map-typed-field counterpart of Test_Decode_NullMergeValue_YAMLNativeErrorSurfaces.
func Test_Decode_NullMergeValueMapField_YAMLNativeErrorSurfaces(t *testing.T) {
	var cfg mapMergeConfig
	err := xcfg.Decode([]byte("modules:\n  <<:\n  a:\n    addr: x\n"), &cfg)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "has no value")
	require.Contains(t, err.Error(), "map merge requires map or sequence of maps as the value")
}

// Test_CheckKnownKeys_MergeIntoMapFieldCheckedAgainstElementType asserts a merged entry is checked against the map's element type at its real dotted path.
func Test_CheckKnownKeys_MergeIntoMapFieldCheckedAgainstElementType(t *testing.T) {
	input := `
base: &b
  a:
    addr: 1.2.3.4
  c:
    addr: 5.6.7.8
    bogus: true
modules:
  <<: *b
`
	err := xcfg.CheckKnownKeys[mapMergeConfig]([]byte(input))
	require.Error(t, err)
	require.Contains(t, err.Error(), "modules.c.bogus")
	require.NotContains(t, err.Error(), "<<")
}

// Test_Decode_MergeSourceNullOverriddenByExplicitKey_Accepted asserts a merge source's null-valued key is unreported once an explicit key overrides it.
func Test_Decode_MergeSourceNullOverriddenByExplicitKey_Accepted(t *testing.T) {
	input := `
base: &b
  a:
modules:
  a:
    addr: x
  <<: *b
`
	var cfg mapMergeConfig
	err := xcfg.Decode([]byte(input), &cfg)
	require.NoError(t, err)
}

// Test_Decode_MergeSequenceEarlierSourceOverridesLaterNull asserts an earlier merge source's key wins over a later source's null for the same key.
func Test_Decode_MergeSequenceEarlierSourceOverridesLaterNull(t *testing.T) {
	input := `
s1: &s1
  a:
    addr: x
s2: &s2
  a:
  b:
modules:
  <<: [*s1, *s2]
`
	var cfg mapMergeConfig
	err := xcfg.Decode([]byte(input), &cfg)
	require.Error(t, err)
	require.NotContains(t, err.Error(), `"modules.a"`)
	require.Contains(t, err.Error(), `"modules.b"`)
}

type shadowMergeInner struct {
	Addr string `yaml:"addr"`
}

type shadowMergeConfig struct {
	Inner shadowMergeInner `yaml:"inner"`
	// Extra swallows the merge anchor's own top-level key so it never reports.
	Extra map[string]yaml.Node `yaml:",inline"`
}

// Test_Decode_MergeSourceUnknownKeyShadowedByExplicitKeyNotDuplicated asserts a merge source's unknown key already bound explicitly is not reported twice.
func Test_Decode_MergeSourceUnknownKeyShadowedByExplicitKeyNotDuplicated(t *testing.T) {
	input := `
base: &b
  bogus: true
inner:
  bogus: kept
  <<: *b
`
	err := xcfg.Decode([]byte(input), new(shadowMergeConfig), xcfg.WithKnownFields())
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown key "inner.bogus"`)
	require.NotContains(t, err.Error(), "unknown keys specified")
}

// Test_Decode_NestedMergeSourceOwnKeyOverridesMergedNull_Accepted asserts a merge source's own explicit key wins over a body it itself merges in as null.
func Test_Decode_NestedMergeSourceOwnKeyOverridesMergedNull_Accepted(t *testing.T) {
	input := `
base: &base
  a:
mid: &mid
  <<: *base
  a:
    addr: real
modules:
  <<: *mid
`
	var cfg mapMergeConfig
	err := xcfg.Decode([]byte(input), &cfg)
	require.NoError(t, err)
}

// Test_Decode_NestedMergeSourceOwnNullOverridesMergedBody_Reported pins the #1947 collapse: a merge source's own null key wins over a body it merges in and is still rejected.
func Test_Decode_NestedMergeSourceOwnNullOverridesMergedBody_Reported(t *testing.T) {
	input := `
base: &base
  a:
    addr: real
mid: &mid
  <<: *base
  a:
modules:
  <<: *mid
`
	var cfg mapMergeConfig
	err := xcfg.Decode([]byte(input), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), `"modules.a"`)
	require.Contains(t, err.Error(), "has no value")
}

// Test_Decode_MergeSourceShadowedSubtreeUnknownKeyNotReported asserts a merged source's whole nested subtree is shadowed, unknown keys inside it included.
func Test_Decode_MergeSourceShadowedSubtreeUnknownKeyNotReported(t *testing.T) {
	input := `
base: &base
  a:
    addr: x
    bogus: true
mid: &mid
  <<: *base
  a:
    addr: y
modules:
  <<: *mid
`
	err := xcfg.Decode([]byte(input), new(mapMergeConfig), xcfg.WithKnownFields())
	require.NoError(t, err)
}

type optionalScalarConfig struct {
	Name xcfg.Optional[string] `yaml:"name"`
}

// Test_Decode_OptionalScalarNull_NotReported asserts a null Optional[string] is left silent, matching a null plain string field.
func Test_Decode_OptionalScalarNull_NotReported(t *testing.T) {
	var cfg optionalScalarConfig
	err := xcfg.Decode([]byte("name:\n"), &cfg)
	require.NoError(t, err)
}

type aliasNullPositionConfig struct {
	Inner knownKeysInner            `yaml:"inner"`
	M     map[string]knownKeysInner `yaml:"m"`
}

// Test_Decode_NullThroughAlias_ReportsAliasSiteLine asserts that a null reached through an alias is reported at the alias site's line, not the anchor's.
func Test_Decode_NullThroughAlias_ReportsAliasSiteLine(t *testing.T) {
	var cfg aliasNullPositionConfig
	err := xcfg.Decode([]byte("inner: &b\nm:\n  k: *b\n"), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), `"m.k" at line 3`)
}

// Test_Decode_UnknownKeyWinsOverNullUnderKnownFields asserts that an unknown-key error takes precedence over a null-value error under WithKnownFields.
func Test_Decode_UnknownKeyWinsOverNullUnderKnownFields(t *testing.T) {
	input := "name: foo\ninner:\nbogus: 1\n"
	var cfg knownKeysConfig
	err := xcfg.Decode([]byte(input), &cfg, xcfg.WithKnownFields())
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus")
	require.NotContains(t, err.Error(), "has no value")
}

type nullArmsConfig struct {
	Optional       xcfg.Optional[knownKeysInner]  `yaml:"optional"`
	Struct         knownKeysInner                 `yaml:"struct"`
	Map            map[string]string              `yaml:"map"`
	Required       xcfg.Required[string]          `yaml:"required"`
	NonEmptyString xcfg.NonEmptyString            `yaml:"nonemptystring"`
	NonZero        xcfg.NonZero[int]              `yaml:"nonzero"`
	Scalar         string                         `yaml:"scalar"`
	Slice          []knownKeysInner               `yaml:"slice"`
	Node           yaml.Node                      `yaml:"node"`
	PtrStruct      *knownKeysInner                `yaml:"ptrstruct"`
	PtrOptional    *xcfg.Optional[knownKeysInner] `yaml:"ptroptional"`
}

type nullArmCase struct {
	name        string
	input       string
	wantErr     bool
	contains    []string
	notContains []string
	seed        func(*nullArmsConfig)
	check       func(*testing.T, *nullArmsConfig)
}

// Test_Decode_NullPositions asserts, per isTransparentToWalk arm, whether a null at that field's position is reported here.
func Test_Decode_NullPositions(t *testing.T) {
	cases := []nullArmCase{
		{
			name:     "optional field reported",
			input:    "optional:\nrequired: r\nnonemptystring: s\nnonzero: 1\n",
			wantErr:  true,
			contains: []string{`"optional"`, "line 1"},
		},
		{
			name:     "struct field reported",
			input:    "struct:\nrequired: r\nnonemptystring: s\nnonzero: 1\n",
			wantErr:  true,
			contains: []string{`"struct"`, "line 1"},
		},
		{
			name:     "map field reported",
			input:    "map:\nrequired: r\nnonemptystring: s\nnonzero: 1\n",
			wantErr:  true,
			contains: []string{`"map"`, "line 1"},
		},
		{
			name:        "required field left to its own Validate error",
			input:       "required:\nnonemptystring: s\nnonzero: 1\n",
			wantErr:     true,
			contains:    []string{"value must be set explicitly"},
			notContains: []string{"no value"},
		},
		{
			name:        "nonemptystring field left to its own Validate error",
			input:       "required: r\nnonemptystring:\nnonzero: 1\n",
			wantErr:     true,
			contains:    []string{"non-empty string is required"},
			notContains: []string{"no value"},
		},
		{
			name:        "nonzero field left to its own Validate error",
			input:       "required: r\nnonemptystring: s\nnonzero:\n",
			wantErr:     true,
			contains:    []string{"non-zero value is required"},
			notContains: []string{"no value"},
		},
		{
			name:  "scalar field not reported",
			input: "required: r\nnonemptystring: s\nnonzero: 1\nscalar:\n",
			seed: func(cfg *nullArmsConfig) {
				cfg.Scalar = "preserved"
			},
			check: func(t *testing.T, cfg *nullArmsConfig) {
				require.Equal(t, "preserved", cfg.Scalar)
			},
		},
		{
			name:     "slice field reported",
			input:    "required: r\nnonemptystring: s\nnonzero: 1\nslice:\n",
			wantErr:  true,
			contains: []string{`"slice"`, "line 4"},
			seed: func(cfg *nullArmsConfig) {
				cfg.Slice = []knownKeysInner{{Addr: "default"}}
			},
			check: func(t *testing.T, cfg *nullArmsConfig) {
				require.Equal(t, []knownKeysInner{{Addr: "default"}}, cfg.Slice)
			},
		},
		{
			name:  "yaml.Node field not reported",
			input: "required: r\nnonemptystring: s\nnonzero: 1\nnode:\n",
		},
		{
			name:  "pointer to struct not reported",
			input: "required: r\nnonemptystring: s\nnonzero: 1\nptrstruct:\n",
		},
		{
			name:  "pointer to Optional[T] not reported",
			input: "required: r\nnonemptystring: s\nnonzero: 1\nptroptional:\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg nullArmsConfig
			if tc.seed != nil {
				tc.seed(&cfg)
			}
			err := xcfg.Decode([]byte(tc.input), &cfg, xcfg.WithKnownFields())
			if !tc.wantErr {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				for _, s := range tc.contains {
					require.Contains(t, err.Error(), s)
				}
				for _, s := range tc.notContains {
					require.NotContains(t, err.Error(), s)
				}
			}
			if tc.check != nil {
				tc.check(t, &cfg)
			}
		})
	}
}

// Test_Decode_EmptyMappingValue_NoNullError asserts that an explicit empty mapping is not treated as null.
func Test_Decode_EmptyMappingValue_NoNullError(t *testing.T) {
	var cfg nullArmsConfig
	err := xcfg.Decode([]byte("required: r\nnonemptystring: s\nnonzero: 1\noptional: {}\n"), &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Optional.Unwrap())
}

type textUnmarshalerConfig struct {
	Addr netip.Addr `yaml:"addr"`
}

// Test_Decode_NullTextUnmarshalerFieldPreservesSeededValue asserts a null against a field decoding through encoding.TextUnmarshaler is left silent, keeping its seeded value.
func Test_Decode_NullTextUnmarshalerFieldPreservesSeededValue(t *testing.T) {
	seeded := netip.MustParseAddr("127.0.0.1")
	cfg := textUnmarshalerConfig{Addr: seeded}
	err := xcfg.Decode([]byte("addr:\n"), &cfg, xcfg.WithKnownFields())
	require.NoError(t, err)
	require.Equal(t, seeded, cfg.Addr)
}

type optionalTextUnmarshalerConfig struct {
	Addr xcfg.Optional[netip.Addr] `yaml:"addr"`
}

// Test_Decode_NullOptionalTextUnmarshalerFieldNotReported asserts the same silence holds through Optional[T].
func Test_Decode_NullOptionalTextUnmarshalerFieldNotReported(t *testing.T) {
	var cfg optionalTextUnmarshalerConfig
	err := xcfg.Decode([]byte("addr:\n"), &cfg, xcfg.WithKnownFields())
	require.NoError(t, err)
}

// Test_Decode_ExplicitMergeTagOnOrdinaryKeyNullReported asserts a key carrying an explicit "!!merge" tag but a non-"<<" name is an ordinary key, so a null there is still reported rather than silently swallowed as a merge source.
func Test_Decode_ExplicitMergeTagOnOrdinaryKeyNullReported(t *testing.T) {
	input := "modules:\n  !!merge a:\n"
	var cfg mapMergeConfig
	err := xcfg.Decode([]byte(input), &cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), `"modules.a"`)
	require.Contains(t, err.Error(), "has no value")
}

// Test_Decode_QuotedMergeKeyStaysOrdinaryKey asserts a quoted "<<" key stays an ordinary map entry rather than being classified as a merge, so its aliased value is checked as an entry body and not spread into the receiving mapping.
func Test_Decode_QuotedMergeKeyStaysOrdinaryKey(t *testing.T) {
	input := "base: &b\n  addr: x\n  bogus: y\nmodules:\n  \"<<\": *b\n"
	err := xcfg.Decode([]byte(input), new(mapMergeConfig), xcfg.WithKnownFields())
	require.Error(t, err)
	require.Contains(t, err.Error(), `"modules.<<.bogus"`)
}
