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
	// inner decodes from a scalar alias into a struct type, which is a
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
	// chain is xcfg.NonEmptyString, which decodes from a bare scalar. There
	// are no keys to check underneath it.
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
	// yaml.v3 decodes an untagged field under its lowercased Go name.
	input := `
memorypath: /dev/hugepages/yanet
`
	require.NoError(t, xcfg.CheckKnownKeys[knownKeysUntagged]([]byte(input)))
}

func Test_CheckKnownKeys_UntaggedFieldOriginalCaseFlagged(t *testing.T) {
	// yaml.v3 silently drops this key rather than decoding it into
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
