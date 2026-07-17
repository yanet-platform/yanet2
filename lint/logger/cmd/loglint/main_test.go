package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeGoFile creates a Go source file with the given content inside a
// temporary directory and returns its path.
func writeGoFile(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "source.go")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	return path
}

// TestLintGoFile verifies lintGoFile against a table of Go source snippets,
// each checked for the exact set of violation names it must produce.
func TestLintGoFile(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		expectedNames []string
	}{
		{
			name: "constructor flagged",
			source: `package foo

import "go.uber.org/zap"

func NewFoo(log *zap.Logger) *int {
	return nil
}
`,
			expectedNames: []string{"NewFoo"},
		},
		{
			name: "method flagged",
			source: `package foo

import "go.uber.org/zap"

type Foo struct{}

func (m *Foo) Configure(log *zap.Logger) error {
	return nil
}
`,
			expectedNames: []string{"Foo.Configure"},
		},
		{
			name: "free function clean",
			source: `package foo

import "go.uber.org/zap"

func Process(log *zap.Logger) error {
	return nil
}
`,
		},
		{
			name: "options-pattern helper clean",
			source: `package foo

import "go.uber.org/zap"

type Option func(*options)

type options struct{}

func WithLog(log *zap.Logger) Option {
	return nil
}
`,
		},
		{
			name: "logger in results clean",
			source: `package foo

import "go.uber.org/zap"

type Config struct{}

func Init(cfg *Config) (*zap.Logger, zap.AtomicLevel, error) {
	return nil, zap.AtomicLevel{}, nil
}
`,
		},
		{
			name: "logger struct field clean",
			source: `package foo

import "go.uber.org/zap"

type Foo struct {
	log *zap.Logger
}

func NewFoo() *Foo {
	return &Foo{}
}
`,
		},
		{
			name: "aliased import flagged",
			source: `package foo

import zaplog "go.uber.org/zap"

func NewFoo(log *zaplog.Logger) *int {
	return nil
}
`,
			expectedNames: []string{"NewFoo"},
		},
		{
			name: "nested callback flagged",
			source: `package foo

import "go.uber.org/zap"

func NewFoo(factory func(*zap.Logger) error) *int {
	return nil
}
`,
			expectedNames: []string{"NewFoo"},
		},
		{
			name: "variadic flagged",
			source: `package foo

import "go.uber.org/zap"

func NewFoo(logs ...*zap.Logger) *int {
	return nil
}
`,
			expectedNames: []string{"NewFoo"},
		},
		{
			name: "interface method flagged",
			source: `package foo

import "go.uber.org/zap"

type Service interface {
	Configure(log *zap.Logger) error
}
`,
			expectedNames: []string{"Service.Configure"},
		},
		{
			name: "generic callback clean",
			source: `package foo

import "go.uber.org/zap"

type Runnable interface{}

func Run[C any](use, short string, factory func(*C, *zap.Logger) (Runnable, error)) error {
	return nil
}
`,
		},
		{
			name: "word-fragment near-misses clean",
			source: `package foo

import "go.uber.org/zap"

func NewsFeed(log *zap.Logger) *int {
	return nil
}

func newlineSplitter(log *zap.Logger) *int {
	return nil
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeGoFile(t, tt.source)

			violations, err := lintGoFile(path)
			require.NoError(t, err)

			var names []string
			for _, v := range violations {
				names = append(names, v.Name)
			}
			require.Equal(t, tt.expectedNames, names)
		})
	}
}

// TestScanSkipsTestFiles verifies that _test.go files are skipped by scan,
// even though the underlying directory walk visits them.
func TestScanSkipsTestFiles(t *testing.T) {
	root := t.TempDir()

	testFile := filepath.Join(root, "foo_test.go")
	require.NoError(t, os.WriteFile(testFile, []byte(`package foo

import "go.uber.org/zap"

func NewFoo(log *zap.Logger) *int { return nil }
`), 0o644))

	violations, err := scan(root, nil)
	require.NoError(t, err)
	require.Empty(t, violations)
}
