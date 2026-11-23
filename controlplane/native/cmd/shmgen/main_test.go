package main

import (
	"debug/dwarf"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// buildFixture compiles the C sources under testdata into one binary with
// the debug information the build gives the views' translation unit, and
// returns its path.
func buildFixture(t *testing.T, sources ...string) string {
	t.Helper()
	return buildFixtureWithFlags(t, nil, sources...)
}

// buildFixtureWithFlags compiles the C sources under testdata with extra
// compiler flags into one binary and returns its path.
func buildFixtureWithFlags(t *testing.T, flags []string, sources ...string) string {
	t.Helper()
	compiler := strings.Fields(os.Getenv("CC"))
	if len(compiler) == 0 {
		compiler = []string{"cc"}
	}
	binary := filepath.Join(t.TempDir(), "fixture")
	arguments := append(slices.Clone(compiler[1:]), flags...)
	arguments = append(arguments, "-g", "-fno-eliminate-unused-debug-types", "-o", binary)
	for _, source := range sources {
		arguments = append(arguments, filepath.Join("testdata", source))
	}
	output, err := exec.Command(compiler[0], arguments...).CombinedOutput()
	require.NoError(t, err, string(output))
	return binary
}

// fixtureOptions returns the options that generate the views of a fixture
// binary into a fresh directory.
func fixtureOptions(t *testing.T, binary string) options {
	t.Helper()
	directory := t.TempDir()
	return options{
		Binary:      binary,
		PackageName: "layout",
		ImportPath:  "example.com/layout",
		Output:      filepath.Join(directory, "layout.go"),
		TestOutput:  filepath.Join(directory, "layout_test.go"),
		Locals:      map[string]bool{},
		Relatives:   map[string]bool{},
	}
}

// Test_Shmgen_FailsOnDisagreeingUnits verifies that a structure two
// compilation units lay out differently fails generation, naming it.
func Test_Shmgen_FailsOnDisagreeingUnits(t *testing.T) {
	binary := buildFixture(t, "disagree_first.c", "disagree_second.c")
	options := fixtureOptions(t, binary)

	err := run(options, []string{"fixture_shared"})
	require.ErrorContains(t, err, "struct fixture_shared: definitions disagree")
	require.NoFileExists(t, options.Output)
}

// Test_Shmgen_KeepsUnchangedOutput verifies that generating the same
// views again leaves the files, their modification times included, as
// they were.
func Test_Shmgen_KeepsUnchangedOutput(t *testing.T) {
	binary := buildFixture(t, "view.c")
	options := fixtureOptions(t, binary)
	require.NoError(t, run(options, []string{"fixture_view"}))
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, path := range []string{options.Output, options.TestOutput} {
		require.NoError(t, os.Chtimes(path, past, past))
	}

	require.NoError(t, run(options, []string{"fixture_view"}))

	for _, path := range []string{options.Output, options.TestOutput} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, past, info.ModTime(), path)
	}
}

// Test_Shmgen_EmitsUnsignedConstants verifies that the enumerators of an
// enumeration named as constants become unsigned constants as wide as
// their enumeration, a 64-bit value with its top bit set included.
func Test_Shmgen_EmitsUnsignedConstants(t *testing.T) {
	binary := buildFixture(t, "constants.c")
	options := fixtureOptions(t, binary)
	options.Constants = []string{"fixture_u64", "fixture_u32"}

	require.NoError(t, run(options, nil))

	source, err := os.ReadFile(options.Output)
	require.NoError(t, err)
	cases := []struct {
		name string
		want string
	}{
		{"64-bit magic", `\bMagic\s+uint64 = 0xdead10cc0000000f\b`},
		{"32-bit version", `\bABIVersion\s+uint32 = 0x34\b`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Regexp(t, tc.want, string(source))
		})
	}
}

// Test_Shmgen_EmitsUnsignedEnumViews verifies that a view of a 64-bit
// unsigned enumeration stays unsigned when a value has its top bit set.
func Test_Shmgen_EmitsUnsignedEnumViews(t *testing.T) {
	binary := buildFixture(t, "constants.c")
	options := fixtureOptions(t, binary)

	require.NoError(t, run(options, []string{"fixture_flagged"}))

	source, err := os.ReadFile(options.Output)
	require.NoError(t, err)
	cases := []struct {
		name string
		want string
	}{
		{"enumeration type", `\btype FixtureFlags uint64\n`},
		{"enumerator value", `\bFixtureFlagTop\s+FixtureFlags = 9223372036854775808\n`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Regexp(t, tc.want, string(source))
		})
	}
}

// Test_Shmgen_TypesPointersByRole verifies that abs_ and local pointers
// become opaque words, a relative pointer to pointers keeps its targets
// absolute, and a flexible array member becomes an accessor.
func Test_Shmgen_TypesPointersByRole(t *testing.T) {
	binary := buildFixture(t, "pointers.c")
	options := fixtureOptions(t, binary)
	options.Locals["fixture_pointers.resolved"] = true
	options.Relatives["fixture_pointers.abs_relative_targets"] = true

	require.NoError(t, run(options, []string{"fixture_pointers"}))

	source, err := os.ReadFile(options.Output)
	require.NoError(t, err)
	cases := []struct {
		name string
		want string
	}{
		{"abs_ pointer", `\bAbsTarget\s+shm\.Local\n`},
		{"local pointer", `\bResolved\s+shm\.Local\n`},
		{"relative pointer to pointers", `\bAbsRelativeTargets\s+shm\.Ptr\[shm\.Local\]\n`},
		{"flexible array accessor", `func \(m \*FixturePointers\) Items\(count int\) \[\]FixtureTarget \{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Regexp(t, tc.want, string(source))
		})
	}
}

// Test_Shmgen_ChecksFlexibleArrayAddress verifies that the generated test
// compares the address a flexible array accessor returns with the offset
// the DWARF gives the member.
func Test_Shmgen_ChecksFlexibleArrayAddress(t *testing.T) {
	binary := buildFixture(t, "pointers.c")
	options := fixtureOptions(t, binary)
	options.Locals["fixture_pointers.resolved"] = true
	options.Relatives["fixture_pointers.abs_relative_targets"] = true

	require.NoError(t, run(options, []string{"fixture_pointers"}))

	test, err := os.ReadFile(options.TestOutput)
	require.NoError(t, err)
	require.Regexp(t, `(?s)\{"FixturePointers\.Items address", func\(\) uintptr \{.*&view\.Items\(1\)\[0\].*\}\(\), 32\}`, string(test))
}

// Test_Shmgen_FailsOnUnmatchedKeys verifies that a local or relative key
// no field of the views takes fails generation, naming the key.
func Test_Shmgen_FailsOnUnmatchedKeys(t *testing.T) {
	cases := []struct {
		name      string
		locals    []string
		relatives []string
		want      string
	}{
		{"unknown local field", []string{"fixture_pointers.missing"}, nil, "-local fixture_pointers.missing"},
		{"unknown relative structure", nil, []string{"fixture_other.abs_targets"}, "-relative fixture_other.abs_targets"},
	}
	binary := buildFixture(t, "pointers.c")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := fixtureOptions(t, binary)
			for _, key := range tc.locals {
				options.Locals[key] = true
			}
			for _, key := range tc.relatives {
				options.Relatives[key] = true
			}

			err := run(options, []string{"fixture_pointers"})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// Test_Shmgen_TypesRelativeKeyWithoutPrefix verifies that a relative key
// makes a pointer to pointers without the abs_ prefix a relative pointer
// to opaque words.
func Test_Shmgen_TypesRelativeKeyWithoutPrefix(t *testing.T) {
	binary := buildFixture(t, "pointers.c")
	options := fixtureOptions(t, binary)
	options.Relatives["fixture_table.targets"] = true

	require.NoError(t, run(options, []string{"fixture_table"}))

	source, err := os.ReadFile(options.Output)
	require.NoError(t, err)
	require.Regexp(t, `\bTargets\s+shm\.Ptr\[shm\.Local\]\n`, string(source))
}

// Test_Shmgen_SkipsLocalFlexibleArrays verifies that a local key naming a
// flexible array of pointers takes it and leaves it without an accessor.
func Test_Shmgen_SkipsLocalFlexibleArrays(t *testing.T) {
	binary := buildFixture(t, "pointers.c")
	options := fixtureOptions(t, binary)
	options.Locals["fixture_table.slots"] = true

	require.NoError(t, run(options, []string{"fixture_table"}))

	source, err := os.ReadFile(options.Output)
	require.NoError(t, err)
	cases := []struct {
		name    string
		pattern string
		present bool
	}{
		{"dataplane-owned comment", `// slots: dataplane-owned flexible array at offset 16\.`, true},
		{"no accessor", `\) Slots\(`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.present {
				require.Regexp(t, tc.pattern, string(source))
			} else {
				require.NotRegexp(t, tc.pattern, string(source))
			}
		})
	}
}

// Test_Shmgen_FailsOnMistypedKeys verifies that a local or relative key
// naming a field of another shape fails generation, naming the field.
func Test_Shmgen_FailsOnMistypedKeys(t *testing.T) {
	cases := []struct {
		name     string
		root     string
		local    string
		relative string
		want     string
	}{
		{
			"local scalar", "fixture_pointers", "fixture_pointers.count", "",
			"struct fixture_pointers: field count is local or relative but no 8-byte pointer",
		},
		{
			"local flexible array of structures", "fixture_pointers", "fixture_pointers.items", "",
			"struct fixture_pointers: field items is local but no flexible array of 8-byte pointers",
		},
		{
			"local and relative flexible array", "fixture_table", "fixture_table.slots", "fixture_table.slots",
			"struct fixture_table: field slots is both local and relative",
		},
		{
			"relative flexible array of pointers", "fixture_table", "", "fixture_table.slots",
			"struct fixture_table: field slots is a flexible array but -relative requires a pointer to 8-byte pointers",
		},
		{
			"local and relative pointer", "fixture_table", "fixture_table.targets", "fixture_table.targets",
			"struct fixture_table: field targets is both local and relative",
		},
		{
			"relative pointer to a structure", "fixture_pointers", "", "fixture_pointers.resolved",
			"struct fixture_pointers: field resolved is relative but no pointer to 8-byte pointers",
		},
	}
	binary := buildFixture(t, "pointers.c")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := fixtureOptions(t, binary)
			if tc.local != "" {
				options.Locals[tc.local] = true
			}
			if tc.relative != "" {
				options.Relatives[tc.relative] = true
			}

			err := run(options, []string{tc.root})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// Test_Shmgen_RejectsImplicitTailPadding verifies that generation fails
// before writing a view whose Go size would change the C array stride.
func Test_Shmgen_RejectsImplicitTailPadding(t *testing.T) {
	binary := buildFixture(t, "packed.c")
	cases := []struct {
		root string
		want string
	}{
		{"fixture_short_tail", "struct fixture_short_tail: size 9 is not a multiple of Go alignment 8"},
		{"fixture_tail_array", "struct fixture_short_tail: size 9 is not a multiple of Go alignment 8"},
		{"fixture_empty_tail", "struct fixture_empty_tail: trailing zero-sized field End would add Go padding"},
	}
	for _, tc := range cases {
		t.Run(tc.root, func(t *testing.T) {
			options := fixtureOptions(t, binary)

			err := run(options, []string{tc.root})
			require.ErrorContains(t, err, tc.want)
			require.NoFileExists(t, options.Output)
			require.NoFileExists(t, options.TestOutput)
		})
	}
}

// Test_Shmgen_PreservesPackedFields verifies that byte storage and a
// flexible array accessor impose no alignment on the enclosing view.
func Test_Shmgen_PreservesPackedFields(t *testing.T) {
	binary := buildFixture(t, "packed.c")
	options := fixtureOptions(t, binary)
	require.NoError(t, run(options, []string{"fixture_packed"}))

	source, err := os.ReadFile(options.Output)
	require.NoError(t, err)
	cases := []struct {
		name string
		want string
	}{
		{"opaque scalar", `\bSample\s+\[16\]byte\b`},
		{"misaligned integer", `\bWord\s+\[4\]byte\b`},
		{"nested flexible array header", `\bHeader\s+FixtureHeader\b`},
		{"flexible array accessor", `func \(m \*FixtureHeader\) Values\(count int\) \[\]uint64`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Regexp(t, tc.want, string(source))
		})
	}
}

// Test_Shmgen_MapsScalarsByEncoding verifies that a scalar becomes the Go
// type of its encoding and size, or raw bytes when Go has none.
func Test_Shmgen_MapsScalarsByEncoding(t *testing.T) {
	binary := buildFixture(t, "scalars.c")
	options := fixtureOptions(t, binary)

	require.NoError(t, run(options, []string{"fixture_scalars"}))

	source, err := os.ReadFile(options.Output)
	require.NoError(t, err)
	cases := []struct {
		name string
		want string
	}{
		{"signed char", `\bExplicitlySigned\s+int8\n`},
		{"plain char", `\bPlain\s+byte\n`},
		{"unsigned char", `\bExplicitlyUnsigned\s+byte\n`},
		{"unsigned anonymous enum", `\bAnonymous\s+uint32\n`},
		{"128-bit integer", `\bWide\s+\[16\]byte\n`},
		{"long double", `\bExtended\s+\[16\]byte\n`},
		{"half float", `\bHalf\s+\[2\]byte\n`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Regexp(t, tc.want, string(source))
		})
	}
}

// Test_Shmgen_FailsOnDisagreeingPointerTargets verifies that a structure
// reached through a pointer, which compilation units define differently,
// fails generation, naming it.
func Test_Shmgen_FailsOnDisagreeingPointerTargets(t *testing.T) {
	binary := buildFixture(t, "reach_first.c", "reach_second.c", "reach_third.c")
	options := fixtureOptions(t, binary)

	err := run(options, []string{"fixture_outer"})
	require.ErrorContains(t, err, "struct fixture_inner: definitions disagree")
}

// Test_Shmgen_KeepsOpaqueStructuresBytes verifies that a pointer to a
// structure the binary never defines points at bytes, while one the
// binary defines once points at its view.
func Test_Shmgen_KeepsOpaqueStructuresBytes(t *testing.T) {
	binary := buildFixture(t, "reach_first.c", "reach_second.c")
	options := fixtureOptions(t, binary)

	require.NoError(t, run(options, []string{"fixture_outer"}))

	source, err := os.ReadFile(options.Output)
	require.NoError(t, err)
	cases := []struct {
		name string
		want string
	}{
		{"defined target", `\bInner\s+shm\.Ptr\[FixtureInner\]\n`},
		{"opaque target", `\bOpaque\s+shm\.Ptr\[byte\]\n`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Regexp(t, tc.want, string(source))
		})
	}
}

// Test_Shmgen_FailsOnNameCollisions verifies that declarations generated
// under one Go name fail generation instead of compiling badly or one of
// them being dropped.
func Test_Shmgen_FailsOnNameCollisions(t *testing.T) {
	cases := []struct {
		name string
		root string
		want string
	}{
		{"accessor and field", "fixture_accessor", "struct FixtureAccessor: the accessor of a flexible array takes the field name Items"},
		{"constant and view", "fixture_named", "enumerator constants take view names: FixtureNode"},
		{"two enumerators", "fixture_twinned", "enum fixture_twins: enumerator fixture_twin: constant FixtureTwin is taken"},
		{"distinct enum tags", "fixture_tag_collision", "enum FixtureTag: definitions disagree"},
	}
	binary := buildFixtureWithFlags(t, []string{"-std=c2x"}, "collisions.c")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := fixtureOptions(t, binary)

			err := run(options, []string{tc.root})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// Test_Shmgen_FailsOnSignedConstants verifies that an enumeration named
// as constants that is signed fails generation, naming it.
func Test_Shmgen_FailsOnSignedConstants(t *testing.T) {
	binary := buildFixture(t, "constants.c")
	options := fixtureOptions(t, binary)
	options.Constants = []string{"fixture_signed"}

	err := run(options, nil)
	require.ErrorContains(t, err, "enum fixture_signed: signed")
}

// Test_Shmgen_RejectsIncompatibleEnums verifies that width or signedness
// conflicts fail generation regardless of which unit is read first.
func Test_Shmgen_RejectsIncompatibleEnums(t *testing.T) {
	cases := []struct {
		name      string
		roots     []string
		constants []string
		want      string
	}{
		{"constants", nil, []string{"fixture_sign"}, "enum fixture_sign: definitions disagree"},
		{"view field", []string{"fixture_sign_holder"}, nil, "enum fixture_sign: definitions disagree"},
		{"typedef field", []string{"fixture_alias_holder"}, nil, "struct fixture_alias_holder: definitions disagree"},
		{"array elements", []string{"fixture_alias_array"}, nil, "struct fixture_alias_array: definitions disagree"},
		{"nested named structure", []string{"fixture_envelope"}, nil, "struct fixture_alias_array: definitions disagree"},
		{"nested anonymous structure", []string{"fixture_anonymous_holder"}, nil, "struct fixture_anonymous_holder: definitions disagree"},
		{"pointer target", []string{"fixture_alias_indirect"}, nil, "struct fixture_alias_indirect: definitions disagree"},
		{"forward width for constants", nil, []string{"fixture_width"}, "enum fixture_width: definitions disagree"},
		{"forward width for a field", []string{"fixture_width_holder"}, nil, "enum fixture_width: definitions disagree"},
	}
	for _, order := range [][]string{
		{"enum_sign_first.c", "enum_sign_second.c"},
		{"enum_sign_second.c", "enum_sign_first.c"},
	} {
		t.Run(order[0], func(t *testing.T) {
			binary := buildFixtureWithFlags(t, []string{"-std=c2x"}, order...)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					options := fixtureOptions(t, binary)
					options.Constants = tc.constants

					err := run(options, tc.roots)
					require.ErrorContains(t, err, tc.want)
					require.NoFileExists(t, options.Output)
				})
			}
		})
	}
}

// Test_Shmgen_ChecksForwardEnumSignedness verifies that a declaration
// constrains signedness only when its debug information records it.
func Test_Shmgen_ChecksForwardEnumSignedness(t *testing.T) {
	cases := []struct {
		name  string
		known bool
	}{
		{"unsigned declaration conflicts with signed definition", true},
		{"unspecified declaration accepts signed definition", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := dwarf.CommonType{ByteSize: 4}
			forward := &dwarf.EnumType{CommonType: base, EnumName: "state"}
			complete := &dwarf.EnumType{
				CommonType: base,
				EnumName:   "state",
				Val:        []*dwarf.EnumValue{{Name: "one", Val: 1}},
			}
			signs := map[*dwarf.EnumType]bool{complete: true}
			if tc.known {
				signs[forward] = false
			}
			m := generator{enums: map[string][]*dwarf.EnumType{"state": {forward, complete}}, signedEnums: signs}

			got, err := m.enumDefinition("state")
			if tc.known {
				require.ErrorContains(t, err, "enum state: definitions disagree")
				return
			}
			require.NoError(t, err)
			require.Same(t, complete, got)
		})
	}
}

// Test_Shmgen_UsesRecordedEnumSignedness verifies that an explicit
// integer type also governs units whose DWARF records only enumerators.
func Test_Shmgen_UsesRecordedEnumSignedness(t *testing.T) {
	cases := []struct {
		name   string
		value  int64
		signed bool
	}{
		{"signed enum with positive values", 1, true},
		{"unsigned enum with its high bit set", -1 << 63, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := &dwarf.EnumType{
				CommonType: dwarf.CommonType{ByteSize: 8},
				Val:        []*dwarf.EnumValue{{Name: "flag", Val: tc.value}},
			}
			explicit := *legacy
			entries := []*dwarf.EnumType{legacy, &explicit}
			for range 2 {
				m := generator{signedEnums: map[*dwarf.EnumType]bool{&explicit: tc.signed}}
				_, err := m.reconcileEnums("", entries)
				require.NoError(t, err)
				require.Equal(t, tc.signed, m.enumSigned(legacy))
				slices.Reverse(entries)
			}
		})
	}
}

// Test_Shmgen_ResolvesForwardEnum verifies that a forward declaration
// takes its signedness and enumerators from the unit that defines it.
func Test_Shmgen_ResolvesForwardEnum(t *testing.T) {
	binary := buildFixtureWithFlags(t, []string{"-std=c2x"}, "enum_sign_first.c", "enum_sign_second.c")
	options := fixtureOptions(t, binary)
	require.NoError(t, run(options, []string{"fixture_forward_holder"}))

	source, err := os.ReadFile(options.Output)
	require.NoError(t, err)
	require.Regexp(t, `\btype FixtureForward int32\b`, string(source))
	require.Regexp(t, `\bFixtureForwardOne\s+FixtureForward = 1\b`, string(source))
}

// Test_Shmgen_RequiresImportForTest verifies that a test file without the
// import path of its package fails before anything is written.
func Test_Shmgen_RequiresImportForTest(t *testing.T) {
	binary := buildFixture(t, "view.c")
	options := fixtureOptions(t, binary)
	options.ImportPath = ""

	err := run(options, []string{"fixture_view"})
	require.ErrorContains(t, err, "-test needs -import")
	require.NoFileExists(t, options.Output)
}

// Test_Shmgen_ImportsUnsafeOnlyForLayoutChecks verifies that the test of
// constants alone does not import a package it does not use.
func Test_Shmgen_ImportsUnsafeOnlyForLayoutChecks(t *testing.T) {
	binary := buildFixture(t, "constants.c")
	options := fixtureOptions(t, binary)
	options.Constants = []string{"fixture_u64"}

	require.NoError(t, run(options, nil))

	test, err := os.ReadFile(options.TestOutput)
	require.NoError(t, err)
	require.NotContains(t, string(test), `"unsafe"`)
}
