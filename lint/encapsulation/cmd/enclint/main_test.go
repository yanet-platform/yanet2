package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeGoFile creates a Go source file with the given content and name
// inside a temporary directory and returns its path.
func writeGoFile(t *testing.T, name, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	return path
}

// TestLintGoFilePrivateAccess verifies rule 1 against a table of Go source
// snippets, each checked for the exact set of selector texts it must flag.
func TestLintGoFilePrivateAccess(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		expectedTexts []string
	}{
		{
			name: "bare m field access clean",
			source: `package foo

type Service struct {
	log int
}

func (m *Service) Run() {
	_ = m.log
}
`,
		},
		{
			name: "chained exported leaf method clean",
			source: `package foo

type Service struct {
	mu int
}

func (m *Service) Run() {
	m.mu.Lock()
}
`,
		},
		{
			name: "chained exported leaf field clean",
			source: `package foo

type Service struct {
	opts int
}

func (m *Service) Run() {
	_ = m.opts.Log
}
`,
		},
		{
			name: "chained unexported leaf on non-bare base flagged",
			source: `package foo

type Service struct {
	opts int
}

func (m *Service) Run() {
	_ = m.opts.log
}
`,
			expectedTexts: []string{"m.opts.log"},
		},
		{
			name: "nested private field flagged",
			source: `package foo

type Service struct {
	service int
}

func (m *Service) Run() {
	_ = m.service.mu
}
`,
			expectedTexts: []string{"m.service.mu"},
		},
		{
			name: "non-m base flagged",
			source: `package foo

func Run(foo *int) {
	_ = foo.bar
}
`,
			expectedTexts: []string{"foo.bar"},
		},
		{
			name: "call result base flagged",
			source: `package foo

func Run() {
	_ = getFoo().bar
}
`,
			expectedTexts: []string{"getFoo().bar"},
		},
		{
			name: "composite literal field key never flagged",
			source: `package foo

func Run(x int) {
	_ = foo{name: x}
}
`,
		},
		{
			name: "exported package selectors clean",
			source: `package foo

import (
	"fmt"

	"go.uber.org/zap"
)

func Run() {
	fmt.Println(zap.String("k", "v"))
}
`,
		},
		{
			name: "deep chain on non-m base flags every level",
			source: `package foo

func Run(a *int) {
	_ = a.b.c
}
`,
			expectedTexts: []string{"a.b.c", "a.b"},
		},
		{
			name: "package-level var initializer flagged",
			source: `package foo

var handler = a.b
`,
			expectedTexts: []string{"a.b"},
		},
		{
			name: "same-type value parameter clean",
			source: `package foo

type uint128 struct {
	hi uint64
	lo uint64
}

func (m uint128) Xor(other uint128) uint128 {
	return uint128{
		hi: m.hi ^ other.hi,
		lo: m.lo ^ other.lo,
	}
}
`,
		},
		{
			name: "same-type pointer parameter with pointer receiver clean",
			source: `package foo

type Service struct {
	log int
}

func (m *Service) Merge(other *Service) {
	m.log = other.log
}
`,
		},
		{
			name: "same-type generic parameter clean",
			source: `package foo

type Set[T any] struct {
	items int
}

func (m *Set[T]) Add(other *Set[T]) {
	m.items = other.items
}
`,
		},
		{
			name: "multi-name same-type parameter field clean",
			source: `package foo

type Point struct {
	x int
}

func (m Point) Pick(a, b Point) int {
	return a.x + b.x
}
`,
		},
		{
			name: "different-type parameter still flagged",
			source: `package foo

type Service struct {
	log int
}

type Other struct {
	log int
}

func (m *Service) Merge(other *Other) {
	_ = other.log
}
`,
			expectedTexts: []string{"other.log"},
		},
		{
			name: "same-named local variable in plain function still flagged",
			source: `package foo

func Run() {
	other := getOther()
	_ = other.log
}
`,
			expectedTexts: []string{"other.log"},
		},
		{
			name: "chained base through same-type parameter still flagged",
			source: `package foo

type Service struct {
	service int
}

func (m *Service) Merge(other *Service) {
	_ = other.service.configs
}
`,
			expectedTexts: []string{"other.service.configs"},
		},
		{
			name: "qualified same-named type parameter still flagged",
			source: `package foo

type Service struct {
	log int
}

func (m *Service) Merge(other pkg.Service) {
	_ = other.log
}
`,
			expectedTexts: []string{"other.log"},
		},
		{
			name: "qualified same-named pointer type parameter still flagged",
			source: `package foo

type Service struct {
	log int
}

func (m *Service) Merge(other *pkg.Service) {
	_ = other.log
}
`,
			expectedTexts: []string{"other.log"},
		},
		{
			name: "receiverless function with same-typed parameter still flagged",
			source: `package foo

type Service struct {
	log int
}

func Run(other Service) {
	_ = other.log
}
`,
			expectedTexts: []string{"other.log"},
		},
		{
			name: "named result of receiver type still flagged",
			source: `package foo

type Service struct {
	log int
}

func (m *Service) Clone() (result *Service) {
	_ = result.log
	return result
}
`,
			expectedTexts: []string{"result.log"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeGoFile(t, "source.go", tt.source)

			violations, testpkgViolations, err := lintGoFile(path, []byte(tt.source))
			require.NoError(t, err)
			require.Empty(t, testpkgViolations)

			var texts []string
			for _, v := range violations {
				texts = append(texts, v.Text)
			}
			require.Equal(t, tt.expectedTexts, texts)
		})
	}
}

// TestLintGoFileEnclosingName verifies that the allowlist key's enclosing
// segment renders "RecvType.MethodName" for a method and the bare name for
// a function.
func TestLintGoFileEnclosingName(t *testing.T) {
	tests := []struct {
		name              string
		source            string
		expectedEnclosing string
	}{
		{
			name: "method enclosing name",
			source: `package foo

type Service struct{}

func (m *Service) Configure() {
	_ = a.b
}
`,
			expectedEnclosing: "Service.Configure",
		},
		{
			name: "function enclosing name",
			source: `package foo

func Configure() {
	_ = a.b
}
`,
			expectedEnclosing: "Configure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeGoFile(t, "source.go", tt.source)

			violations, _, err := lintGoFile(path, []byte(tt.source))
			require.NoError(t, err)
			require.Len(t, violations, 1)
			require.Equal(t, path+":"+tt.expectedEnclosing+":a.b", violations[0].Key)
		})
	}
}

// TestLintGoFileSkipsCgoFile verifies that a file importing "C" is skipped
// entirely by rule 1, since C struct fields are syntactically
// indistinguishable from Go private fields without type information.
func TestLintGoFileSkipsCgoFile(t *testing.T) {
	source := `package foo

// #include <stdlib.h>
import "C"

func Run() {
	_ = C.some_struct.private_field
}
`
	path := writeGoFile(t, "cgo.go", source)

	violations, testpkgViolations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, violations)
	require.Empty(t, testpkgViolations)
}

// TestLintGoFileSkipsGeneratedFile verifies that a file carrying the
// standard "generated code" marker is skipped by both rules.
func TestLintGoFileSkipsGeneratedFile(t *testing.T) {
	source := `// Code generated by protoc-gen-go. DO NOT EDIT.
package foo

func Run() {
	_ = a.b
}
`
	path := writeGoFile(t, "source.pb.go", source)

	violations, testpkgViolations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, violations)
	require.Empty(t, testpkgViolations)
}

// TestLintGoFileSkipsPbGoFile verifies that a .pb.go file is skipped by
// rule 1 even without a generated-code marker.
func TestLintGoFileSkipsPbGoFile(t *testing.T) {
	source := `package foo

func Run() {
	_ = a.b
}
`
	path := writeGoFile(t, "source.pb.go", source)

	violations, testpkgViolations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, violations)
	require.Empty(t, testpkgViolations)
}

// TestLintGoFileSkipsTestFileForRule1 verifies that a _test.go file is
// never checked by rule 1, even when it reaches into a private field on a
// non-m base.
func TestLintGoFileSkipsTestFileForRule1(t *testing.T) {
	source := `package foo_test

func TestRun(t *testing.T) {
	_ = a.b
}
`
	path := writeGoFile(t, "source_test.go", source)

	violations, _, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, violations)
}

// TestLintGoFileFlagsInternalTestPackage verifies rule 2 flags a _test.go
// file whose package clause does not end in "_test".
func TestLintGoFileFlagsInternalTestPackage(t *testing.T) {
	source := `package foo

func TestRun(t *testing.T) {}
`
	path := writeGoFile(t, "source_test.go", source)

	violations, testpkgViolations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, violations)
	require.Len(t, testpkgViolations, 1)
	require.Equal(t, "foo", testpkgViolations[0].Pkg)
	require.Equal(t, path+":foo", testpkgViolations[0].Key)
}

// TestLintGoFileAcceptsExternalTestPackage verifies rule 2 stays clean for
// a _test.go file whose package clause ends in "_test".
func TestLintGoFileAcceptsExternalTestPackage(t *testing.T) {
	source := `package foo_test

func TestRun(t *testing.T) {}
`
	path := writeGoFile(t, "source_test.go", source)

	_, testpkgViolations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, testpkgViolations)
}

// TestLintGoFileAcceptsMainTestPackage verifies rule 2 stays clean for a
// _test.go file declaring "package main", since a main package is
// unimportable and an external test package for it could never reach
// anything, while a non-main internal package with the same shape is
// still flagged.
func TestLintGoFileAcceptsMainTestPackage(t *testing.T) {
	source := `package main

func TestRun(t *testing.T) {}
`
	path := writeGoFile(t, "source_test.go", source)

	_, testpkgViolations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, testpkgViolations)
}
