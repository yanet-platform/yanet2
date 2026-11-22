package main

import (
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
	compiler := strings.Fields(os.Getenv("CC"))
	if len(compiler) == 0 {
		compiler = []string{"cc"}
	}
	binary := filepath.Join(t.TempDir(), "fixture")
	arguments := append(slices.Clone(compiler[1:]), "-g", "-fno-eliminate-unused-debug-types", "-o", binary)
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
	}
	binary := buildFixture(t, "collisions.c")
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
