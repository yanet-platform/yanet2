package main

import (
	"os"
	"path/filepath"
	"strings"
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

// checksOf returns the Check field of every violation, in order, for
// comparison against a table's expected check sequence.
func checksOf(violations []violation) []string {
	var checks []string
	for _, v := range violations {
		checks = append(checks, v.Check)
	}
	return checks
}

// namesForCheck returns the allowlist-key name segment (everything after
// "<check>:<path>:") of every violation matching check, in order.
func namesForCheck(t *testing.T, violations []violation, check, path string) []string {
	t.Helper()

	prefix := check + ":" + path + ":"
	var names []string
	for _, v := range violations {
		if v.Check != check {
			continue
		}
		require.True(t, strings.HasPrefix(v.Key, prefix), "key %q missing prefix %q", v.Key, prefix)
		names = append(names, strings.TrimPrefix(v.Key, prefix))
	}
	return names
}

// privateTexts returns the rendered selector text (the fourth ":"-separated
// segment) of every "private" violation, in order. The enclosing name and
// the text may themselves contain "." but never ":", so a 4-way split
// isolates the text unambiguously.
func privateTexts(t *testing.T, violations []violation) []string {
	t.Helper()

	var texts []string
	for _, v := range violations {
		if v.Check != "private" {
			continue
		}
		parts := strings.SplitN(v.Key, ":", 4)
		require.Len(t, parts, 4)
		texts = append(texts, parts[3])
	}
	return texts
}

// TestLintGoFileLoggerParam verifies the logger check against a table of Go
// source snippets, each checked for the exact set of names it must flag.
func TestLintGoFileLoggerParam(t *testing.T) {
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
			path := writeGoFile(t, "source.go", tt.source)

			violations, err := lintGoFile(path, []byte(tt.source))
			require.NoError(t, err)

			names := namesForCheck(t, violations, "logger", path)
			require.Equal(t, tt.expectedNames, names)
		})
	}
}

// TestScanSkipsTestFilesForLogger verifies that a _test.go file never
// triggers the logger check, even though the underlying directory walk
// visits it and the file is linted for other checks.
func TestScanSkipsTestFilesForLogger(t *testing.T) {
	root := t.TempDir()

	testFile := filepath.Join(root, "foo_test.go")
	require.NoError(t, os.WriteFile(testFile, []byte(`package foo_test

import "go.uber.org/zap"

func NewFoo(log *zap.Logger) *int { return nil }
`), 0o644))

	violations, err := scan(root, nil)
	require.NoError(t, err)
	require.Empty(t, namesForCheck(t, violations, "logger", testFile))
}

// TestLintGoFilePrivateAccess verifies the private check against a table of
// Go source snippets, each checked for the exact set of selector texts it
// must flag.
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

			violations, err := lintGoFile(path, []byte(tt.source))
			require.NoError(t, err)

			require.Equal(t, tt.expectedTexts, privateTexts(t, violations))
		})
	}
}

// TestLintGoFileEnclosingName verifies that the private check's allowlist
// key renders "RecvType.MethodName" for a method and the bare name for a
// function.
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

			violations, err := lintGoFile(path, []byte(tt.source))
			require.NoError(t, err)

			names := namesForCheck(t, violations, "private", path)
			require.Equal(t, []string{tt.expectedEnclosing + ":a.b"}, names)
		})
	}
}

// TestLintGoFileSkipsCgoFile verifies that a file importing "C" is skipped
// by the private check, since C struct fields are syntactically
// indistinguishable from Go private fields without type information, while
// the receiver check — unrelated to the cgo exemption — still applies.
func TestLintGoFileSkipsCgoFile(t *testing.T) {
	source := `package foo

// #include <stdlib.h>
import "C"

type Foo struct{}

func (f *Foo) Run() {
	_ = C.some_struct.private_field
}
`
	path := writeGoFile(t, "cgo.go", source)

	violations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)

	require.Empty(t, namesForCheck(t, violations, "private", path))
	require.Equal(t, []string{"Foo.Run"}, namesForCheck(t, violations, "receiver", path))
}

// TestLintGoFileSkipsGeneratedFile verifies that a file carrying the
// standard "generated code" marker is skipped by every check.
func TestLintGoFileSkipsGeneratedFile(t *testing.T) {
	source := `// Code generated by protoc-gen-go. DO NOT EDIT.
package foo

func Run() {
	_ = a.b
}
`
	path := writeGoFile(t, "source.pb.go", source)

	violations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, violations)
}

// TestLintGoFileSkipsPbGoFile verifies that a .pb.go file is skipped by
// every check even without a generated-code marker.
func TestLintGoFileSkipsPbGoFile(t *testing.T) {
	source := `package foo

func Run() {
	_ = a.b
}
`
	path := writeGoFile(t, "source.pb.go", source)

	violations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, violations)
}

// TestLintGoFileSkipsTestFileForPrivate verifies that a _test.go file is
// never checked by the private check, even when it reaches into a private
// field on a non-m base, since a _test.go file is checked by testpkg
// instead.
func TestLintGoFileSkipsTestFileForPrivate(t *testing.T) {
	source := `package foo_test

func TestRun(t *testing.T) {
	_ = a.b
}
`
	path := writeGoFile(t, "source_test.go", source)

	violations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, namesForCheck(t, violations, "private", path))
}

// TestLintGoFileFlagsInternalTestPackage verifies the testpkg check flags a
// _test.go file whose package clause does not end in "_test".
func TestLintGoFileFlagsInternalTestPackage(t *testing.T) {
	source := `package foo

func TestRun(t *testing.T) {}
`
	path := writeGoFile(t, "source_test.go", source)

	violations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)

	names := namesForCheck(t, violations, "testpkg", path)
	require.Equal(t, []string{"foo"}, names)
}

// TestLintGoFileAcceptsExternalTestPackage verifies the testpkg check stays
// clean for a _test.go file whose package clause ends in "_test".
func TestLintGoFileAcceptsExternalTestPackage(t *testing.T) {
	source := `package foo_test

func TestRun(t *testing.T) {}
`
	path := writeGoFile(t, "source_test.go", source)

	violations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, namesForCheck(t, violations, "testpkg", path))
}

// TestLintGoFileAcceptsMainTestPackage verifies the testpkg check stays
// clean for a _test.go file declaring "package main", since a main package
// is unimportable and an external test package for it could never reach
// anything, while a non-main internal package with the same shape is still
// flagged.
func TestLintGoFileAcceptsMainTestPackage(t *testing.T) {
	source := `package main

func TestRun(t *testing.T) {}
`
	path := writeGoFile(t, "source_test.go", source)

	violations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)
	require.Empty(t, namesForCheck(t, violations, "testpkg", path))
}

// TestLintGoFileStyleChecks verifies the eleven checks receiver, loopindex,
// maplit, grpcdial, sugar, zapmsg, zapkey, testctx, handlerblank, barenew,
// and loggerlast against a table of Go source snippets, each checked for
// the exact sequence of checks it must trigger. Every one of the eleven
// checks has a failing case that triggers it and a passing case that does
// not.
func TestLintGoFileStyleChecks(t *testing.T) {
	tests := []struct {
		name           string
		fileName       string
		source         string
		expectedChecks []string
	}{
		{
			name: "receiver wrong name flagged",
			source: `package foo

type Foo struct{}

func (f *Foo) Run() {}
`,
			expectedChecks: []string{"receiver"},
		},
		{
			name: "receiver unnamed clean",
			source: `package foo

type Foo struct{}

func (*Foo) Run() {}
`,
		},
		{
			name: "receiver blank identifier flagged",
			source: `package foo

type Foo struct{}

func (_ *Foo) Run() {}
`,
			expectedChecks: []string{"receiver"},
		},
		{
			name: "receiver named m clean",
			source: `package foo

type Foo struct{}

func (m *Foo) Run() {}
`,
		},
		{
			name: "loopindex for-loop i flagged",
			source: `package foo

func Run() {
	for i := 0; i < 10; i++ {
		_ = i
	}
}
`,
			expectedChecks: []string{"loopindex"},
		},
		{
			name: "loopindex range i flagged",
			source: `package foo

func Run() {
	for i := range 10 {
		_ = i
	}
}
`,
			expectedChecks: []string{"loopindex"},
		},
		{
			name: "loopindex for-loop assigned (non-declared) i flagged",
			source: `package foo

func Run() {
	var i int
	for i = 0; i < 10; i++ {
		_ = i
	}
}
`,
			expectedChecks: []string{"loopindex"},
		},
		{
			name: "loopindex range idx clean",
			source: `package foo

func Run() {
	for idx := range 10 {
		_ = idx
	}
}
`,
		},
		{
			name: "maplit single-arg make flagged",
			source: `package foo

func Run() {
	_ = make(map[string]int)
}
`,
			expectedChecks: []string{"maplit"},
		},
		{
			name: "maplit two-arg make with capacity hint clean",
			source: `package foo

func Run() {
	_ = make(map[string]int, 10)
}
`,
		},
		{
			name: "grpcdial flagged",
			source: `package foo

import "google.golang.org/grpc"

func Run() {
	_, _ = grpc.Dial("addr")
}
`,
			expectedChecks: []string{"grpcdial"},
		},
		{
			name: "grpcdial DialContext flagged",
			source: `package foo

import (
	"context"

	"google.golang.org/grpc"
)

func Run(ctx context.Context) {
	_, _ = grpc.DialContext(ctx, "addr")
}
`,
			expectedChecks: []string{"grpcdial"},
		},
		{
			name: "grpc NewClient clean",
			source: `package foo

import "google.golang.org/grpc"

func Run() {
	_, _ = grpc.NewClient("addr")
}
`,
		},
		{
			name: "sugar type reference flagged",
			source: `package foo

import "go.uber.org/zap"

func Run(log zap.SugaredLogger) {
	_ = log
}
`,
			expectedChecks: []string{"sugar"},
		},
		{
			name: "sugar Sugar call flagged",
			source: `package foo

import "go.uber.org/zap"

func Run(log *zap.Logger) {
	_ = log.Sugar()
}
`,
			expectedChecks: []string{"sugar"},
		},
		{
			name: "zap Logger pointer clean",
			source: `package foo

import "go.uber.org/zap"

func Run(log *zap.Logger) {
	_ = log
}
`,
		},
		{
			name: "zapmsg uppercase message flagged",
			source: `package foo

import "go.uber.org/zap"

func Run(log *zap.Logger) {
	log.Info("Something happened")
}
`,
			expectedChecks: []string{"zapmsg"},
		},
		{
			name: "zapmsg lowercase message clean",
			source: `package foo

import "go.uber.org/zap"

func Run(log *zap.Logger) {
	log.Info("something happened")
}
`,
		},
		{
			name: "zapmsg testing.T failure message clean",
			source: `package foo

import (
	"testing"

	"go.uber.org/zap"
)

func TestRun(t *testing.T) {
	log := zap.NewNop()
	t.Fatal("Run did not return within the grace period")
	t.Error("IsAnonymous = false, want true")
	log.Info("something happened")
}
`,
		},
		{
			name: "zapmsg testing.B failure message clean",
			source: `package foo

import (
	"testing"

	"go.uber.org/zap"
)

func BenchmarkRun(b *testing.B) {
	log := zap.NewNop()
	b.Fatal("No records read")
	log.Info("something happened")
}
`,
		},
		{
			name: "zapmsg Fatal on a real logger flagged",
			source: `package foo

import "go.uber.org/zap"

func Run(log *zap.Logger) {
	log.Fatal("Configuration is invalid")
}
`,
			expectedChecks: []string{"zapmsg"},
		},
		{
			name: "zapmsg Error on a real logger flagged",
			source: `package foo

import "go.uber.org/zap"

func Run(log *zap.Logger) {
	log.Error("Request failed")
}
`,
			expectedChecks: []string{"zapmsg"},
		},
		{
			name: "zapkey camelCase key flagged",
			source: `package foo

import "go.uber.org/zap"

func Run(log *zap.Logger) {
	log.Info("message", zap.Bool("isTimeout", true))
}
`,
			expectedChecks: []string{"zapkey"},
		},
		{
			name: "zapkey snake_case key clean",
			source: `package foo

import "go.uber.org/zap"

func Run(log *zap.Logger) {
	log.Info("message", zap.Bool("is_timeout", true))
}
`,
		},
		{
			name: "zapkey ast.IsExported guard skips a zap-shadowing local's unexported method",
			source: `package foo

import "go.uber.org/zap"

type zapShadow struct{}

func (zapShadow) probe(key string) {}

func Run() {
	zap := zapShadow{}
	zap.probe("weirdKey")
}
`,
			expectedChecks: []string{"private"},
		},
		{
			name:     "testctx Background in test file flagged",
			fileName: "source_test.go",
			source: `package foo_test

import (
	"context"
	"testing"
)

func TestRun(t *testing.T) {
	ctx := context.Background()
	_ = ctx
}
`,
			expectedChecks: []string{"testctx"},
		},
		{
			name:     "testctx TODO in test file flagged",
			fileName: "source_test.go",
			source: `package foo_test

import (
	"context"
	"testing"
)

func TestRun(t *testing.T) {
	ctx := context.TODO()
	_ = ctx
}
`,
			expectedChecks: []string{"testctx"},
		},
		{
			name:     "testctx t.Context in test file clean",
			fileName: "source_test.go",
			source: `package foo_test

import "testing"

func TestRun(t *testing.T) {
	ctx := t.Context()
	_ = ctx
}
`,
		},
		{
			name: "handlerblank blank context parameter flagged",
			source: `package foo

import "context"

type Service struct{}

func (m *Service) Handle(_ context.Context) error {
	return nil
}
`,
			expectedChecks: []string{"handlerblank"},
		},
		{
			name: "handlerblank named context parameter clean",
			source: `package foo

import "context"

type Service struct{}

func (m *Service) Handle(ctx context.Context) error {
	_ = ctx
	return nil
}
`,
		},
		{
			name: "handlerblank blank pb-suffixed parameter flagged",
			source: `package foo

import "example.com/foopb"

type Service struct{}

func (m *Service) Handle(_ *foopb.Request) error {
	return nil
}
`,
			expectedChecks: []string{"handlerblank"},
		},
		{
			name: "handlerblank named pb-suffixed parameter clean",
			source: `package foo

import "example.com/foopb"

type Service struct{}

func (m *Service) Handle(req *foopb.Request) error {
	_ = req
	return nil
}
`,
		},
		{
			name: "handlerblank matches by import alias suffix alone, not by an actual protobuf type",
			source: `package foo

import scrubpb "example.com/scrub"

type Service struct{}

func (m *Service) Handle(_ *scrubpb.Options) error {
	return nil
}
`,
			expectedChecks: []string{"handlerblank"},
		},
		{
			name: "handlerblank blank versioned pb-suffixed parameter flagged",
			source: `package foo

import "example.com/modules/foo/controlplane/foopb/v1"

type Service struct{}

func (m *Service) Handle(_ *foopb.Request) error {
	return nil
}
`,
			expectedChecks: []string{"handlerblank"},
		},
		{
			name: "barenew flagged",
			source: `package foo

func New() *int {
	return nil
}
`,
			expectedChecks: []string{"barenew"},
		},
		{
			name: "NewFoo constructor clean",
			source: `package foo

func NewFoo() *int {
	return nil
}
`,
		},
		{
			name: "method named New clean",
			source: `package foo

type Foo struct{}

func (m *Foo) New() *Foo {
	return m
}
`,
		},
		{
			name: "loggerlast field not last flagged",
			source: `package foo

import "go.uber.org/zap"

type Service struct {
	log  *zap.Logger
	name string
}
`,
			expectedChecks: []string{"loggerlast"},
		},
		{
			name: "loggerlast field last clean",
			source: `package foo

import "go.uber.org/zap"

type Service struct {
	name string
	log  *zap.Logger
}
`,
		},
		{
			name: "loggerlast embedded field not last flagged",
			source: `package foo

import "go.uber.org/zap"

type Service struct {
	*zap.Logger
	name string
}
`,
			expectedChecks: []string{"loggerlast"},
		},
		{
			name: "loggerlast embedded field last clean",
			source: `package foo

import "go.uber.org/zap"

type Service struct {
	name string
	*zap.Logger
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fileName := tt.fileName
			if fileName == "" {
				fileName = "source.go"
			}

			path := writeGoFile(t, fileName, tt.source)
			content, err := os.ReadFile(path)
			require.NoError(t, err)

			violations, err := lintGoFile(path, content)
			require.NoError(t, err)

			require.Equal(t, tt.expectedChecks, checksOf(violations))
		})
	}
}

// TestLintGenDeclAppliesInTestFiles verifies that a package-level
// declaration in a _test.go file is linted like any other file for a check
// with no test-file exemption (loggerlast), alongside the testpkg check
// that _test.go files themselves are subject to.
func TestLintGenDeclAppliesInTestFiles(t *testing.T) {
	path := writeGoFile(t, "source_test.go", `package foo_test

import "go.uber.org/zap"

type Service struct {
	log  *zap.Logger
	name string
}
`)
	content, err := os.ReadFile(path)
	require.NoError(t, err)

	violations, err := lintGoFile(path, content)
	require.NoError(t, err)
	require.Equal(t, []string{"loggerlast"}, checksOf(violations))
}

// TestScanSkipsGeneratedFiles verifies that a .pb.go file is never linted,
// even when its content would otherwise trigger a violation.
func TestScanSkipsGeneratedFiles(t *testing.T) {
	root := t.TempDir()

	genFile := filepath.Join(root, "source.pb.go")
	require.NoError(t, os.WriteFile(genFile, []byte(`package foo

func New() *int { return nil }
`), 0o644))

	violations, err := scan(root, nil)
	require.NoError(t, err)
	require.Empty(t, violations)
}

// TestScanZapEligibilityCrossesFiles verifies that zapmsg and sugar flag a
// method in a file that does not itself import zap, when a sibling file in
// the same package declares the *zap.Logger field the method logs through.
func TestScanZapEligibilityCrossesFiles(t *testing.T) {
	root := t.TempDir()

	structFile := filepath.Join(root, "service.go")
	require.NoError(t, os.WriteFile(structFile, []byte(`package foo

import "go.uber.org/zap"

type Service struct {
	name string
	log  *zap.Logger
}
`), 0o644))

	methodFile := filepath.Join(root, "handle.go")
	require.NoError(t, os.WriteFile(methodFile, []byte(`package foo

func (m *Service) Handle() {
	m.log.Info("Started")
	sugared := m.log.Sugar()
	_ = sugared
}
`), 0o644))

	violations, err := scan(root, nil)
	require.NoError(t, err)

	require.Equal(t, []string{"Service.Handle:Info:Started"}, namesForCheck(t, violations, "zapmsg", methodFile))
	require.Equal(t, []string{"Service.Handle:Sugar()"}, namesForCheck(t, violations, "sugar", methodFile))
}

// TestLintGoFileTestFileStillChecksReceiver verifies that a _test.go file,
// while exempt from logger and private, is still checked by receiver — a
// convention with no test-code relaxation.
func TestLintGoFileTestFileStillChecksReceiver(t *testing.T) {
	source := `package foo_test

import "go.uber.org/zap"

type Foo struct{}

func (f *Foo) NewBar(log *zap.Logger) *int {
	return nil
}
`
	path := writeGoFile(t, "source_test.go", source)

	violations, err := lintGoFile(path, []byte(source))
	require.NoError(t, err)

	require.Equal(t, []string{"Foo.NewBar"}, namesForCheck(t, violations, "receiver", path))
	require.Empty(t, namesForCheck(t, violations, "logger", path))
}

// TestWithOccurrenceOrdinals verifies that withOccurrenceOrdinals appends a
// 1-based occurrence ordinal to each violation's Key, counted among the
// violations sharing that Key, in encounter order.
func TestWithOccurrenceOrdinals(t *testing.T) {
	tests := []struct {
		name         string
		violations   []violation
		expectedKeys []string
	}{
		{
			name: "distinct keys each start at one",
			violations: []violation{
				{Key: "loopindex:foo.go:Run:i"},
				{Key: "loopindex:bar.go:Run:i"},
			},
			expectedKeys: []string{
				"loopindex:foo.go:Run:i:1",
				"loopindex:bar.go:Run:i:1",
			},
		},
		{
			name: "repeated key numbered in encounter order",
			violations: []violation{
				{Key: "loopindex:foo.go:Run:i"},
				{Key: "loopindex:foo.go:Run:i"},
				{Key: "loopindex:foo.go:Run:i"},
			},
			expectedKeys: []string{
				"loopindex:foo.go:Run:i:1",
				"loopindex:foo.go:Run:i:2",
				"loopindex:foo.go:Run:i:3",
			},
		},
		{
			name: "interleaved keys counted independently",
			violations: []violation{
				{Key: "loopindex:foo.go:Run:i"},
				{Key: "private:foo.go:Run:m.log"},
				{Key: "loopindex:foo.go:Run:i"},
			},
			expectedKeys: []string{
				"loopindex:foo.go:Run:i:1",
				"private:foo.go:Run:m.log:1",
				"loopindex:foo.go:Run:i:2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withOrdinals := withOccurrenceOrdinals(tt.violations)

			var keys []string
			for _, v := range withOrdinals {
				keys = append(keys, v.Key)
			}
			require.Equal(t, tt.expectedKeys, keys)
		})
	}
}

// TestReportKeysEachOccurrenceIndependently verifies, at the report() level,
// the two properties the occurrence ordinal exists to provide: a violation
// count beyond what the allowlist covers is still reported live, and fixing
// some but not all of several identical violations leaves the row for the
// now-missing occurrence stale.
func TestReportKeysEachOccurrenceIndependently(t *testing.T) {
	makeViolations := func(count int) []violation {
		violations := make([]violation, count)
		for idx := range violations {
			violations[idx] = violation{
				Check:   "loopindex",
				Key:     "loopindex:foo.go:Run:i",
				Path:    "foo.go",
				Line:    idx + 1,
				Message: "foo.go loop index must be idx, not i",
			}
		}
		return violations
	}

	allowlistPath := filepath.Join(t.TempDir(), "allowlist.txt")
	require.NoError(t, os.WriteFile(allowlistPath, []byte(
		"loopindex:foo.go:Run:i:1  # pre-existing\n"+
			"loopindex:foo.go:Run:i:2  # pre-existing\n"+
			"loopindex:foo.go:Run:i:3  # pre-existing\n",
	), 0o644))

	require.False(t, report(makeViolations(3), allowlistPath), "three known occurrences must stay green")
	require.True(t, report(makeViolations(4), allowlistPath), "a fourth occurrence must not be suppressed by rows covering the first three")
	require.True(t, report(makeViolations(2), allowlistPath), "fixing one of three occurrences must leave the third row stale")
}
