// Command stylelint enforces this repository's Go micro-conventions from
// CLAUDE.md in a single pass over the source tree.
//
// It runs fourteen checks sharing one allowlist, lint/style/allowlist.txt,
// keyed "<check>:<path>:<name>:<ordinal>" where <check> is one of:
//
//   - logger — a constructor or method must not accept a zap logger as a
//     parameter; it goes through the options pattern (WithLog) instead.
//   - private — a private field or method reached through any base other
//     than the receiver m.
//   - testpkg — a _test.go file declaring an internal (non-_test) package.
//   - receiver, loopindex, maplit, grpcdial, sugar, zapmsg, zapkey, testctx,
//     handlerblank, barenew, loggerlast — see their declarations below for
//     what each one enforces.
//
// <name> is the enclosing function or method ("RecvType.MethodName" or
// "FuncName"), a struct field ("TypeName.FieldName"), a package-level
// var/const, or the enclosing name followed by additional ":"-separated
// segments identifying the offending site, when several violations of one
// check can share an enclosing declaration. The private check's key already
// carries an extra segment of its own ("<path>:<func>:<expr>"), which the
// leading "private:" prefix does not disturb: the allowlist only requires a
// ":" to be present somewhere in the key.
//
// <ordinal> is a 1-based count of the violation's position, in source
// order, among every other violation sharing the same
// "<check>:<path>:<name>" prefix; see withOccurrenceOrdinals. It keeps
// several structurally identical violations inside one function from
// collapsing onto a single allowlist row: each occurrence gets its own row,
// so a newly introduced occurrence is never suppressed by a row that
// already covers an earlier one, and fixing some but not all occurrences
// leaves the remaining ones' row reported as stale.
//
// Known violations are tracked in the allowlist so that paying down the
// debt is enforced by deleting the corresponding row.
//
// Each check's scope (which files and declarations it applies to) is
// documented at the check's own declaration, since the scopes are not
// uniform: some checks skip _test.go files, cgo sources, or generated code
// for reasons specific to that check.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/yanet-platform/yanet2/lint/internal/gosrc"
	"github.com/yanet-platform/yanet2/lint/internal/ledger"
)

// zapImportPath is the import path of the structured logging package.
const zapImportPath = "go.uber.org/zap"

// contextImportPath is the import path of the standard context package.
const contextImportPath = "context"

// grpcImportPath is the import path of the gRPC package.
const grpcImportPath = "google.golang.org/grpc"

// violation describes one checked site that violates a style convention.
type violation struct {
	// Check is the stable check name, e.g. "receiver".
	Check string
	// Key is the stable allowlist key: "<check>:<path>:<name>".
	Key string
	// Path is the file path, relative to the repo root and slash-separated.
	Path string
	// Line is the 1-based source line of the violation.
	Line int
	// Message is the human-readable diagnostic printed for a live violation.
	Message string
}

// fileContext carries the per-file state shared by every check applied to
// one parsed Go file.
//
// Every field is exported: fileContext is a plain data carrier threaded
// through free functions rather than a type with methods and an invariant
// to protect, so there is no encapsulation boundary for a private field to
// cross.
type fileContext struct {
	// Path is the file path, relative to the repo root and slash-separated.
	Path string
	// Fset resolves AST positions to line numbers for this file.
	Fset *token.FileSet
	// IsTest reports whether Path is a _test.go file.
	IsTest bool
	// IsCgo reports whether the file imports the pseudo-package "C".
	IsCgo bool
	// ZapAlias is the local identifier bound to the zap import, or the
	// default "zap" when this file has no zap import of its own but
	// ZapEligible is true — see the package-scoped eligibility note below.
	ZapAlias string
	// HasZap reports whether this specific file imports zap.
	HasZap bool
	// ZapEligible reports whether any non-generated file in this file's
	// directory imports zap, making the zapmsg, zapkey, sugar, and
	// loggerlast checks apply to this file even when it does not import
	// zap itself — a method can call a *zap.Logger field declared on a
	// struct in a sibling file of the same package.
	ZapEligible bool
	// ContextAlias is the local identifier bound to the context import.
	ContextAlias string
	// HasContext reports whether the file imports context.
	HasContext bool
	// GRPCAlias is the local identifier bound to the grpc import.
	GRPCAlias string
	// HasGRPC reports whether the file imports grpc.
	HasGRPC bool
	// PBAliases is the set of local import identifiers ending in "pb".
	PBAliases map[string]bool
}

func main() {
	var excludes gosrc.ExcludeList
	flag.Var(&excludes, "exclude", "directory to exclude (may be repeated)")
	allowlistPath := flag.String("allowlist", "lint/style/allowlist.txt", "path to the allowlist file")
	flag.Parse()

	violations, err := scan(".", excludes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error scanning: %v\n", err)
		os.Exit(1)
	}

	if report(violations, *allowlistPath) {
		os.Exit(1)
	}
}

// parsedGoFile carries one successfully parsed, in-scope Go source file
// between scan's directory-eligibility pre-pass and its linting pass.
type parsedGoFile struct {
	Path string
	File *ast.File
	Fset *token.FileSet
}

// scan walks the directory tree rooted at root and returns every violation
// found in non-excluded, non-generated Go files.
//
// It runs two passes over the tree: the first parses every in-scope file
// and records, per directory, whether any file there imports zap; the
// second lints every file with that directory's eligibility already known,
// so that zapmsg, zapkey, sugar, and loggerlast apply to a file even when
// only a sibling file in the same package imports zap.
func scan(root string, excludes gosrc.ExcludeList) ([]violation, error) {
	var files []parsedGoFile
	dirHasZap := map[string]bool{}

	err := gosrc.Walk(root, excludes, func(path string) error {
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		file, fset, err := parseGoFile(path, content)
		if err != nil {
			return fmt.Errorf("failed to lint %s: %w", path, err)
		}
		if file == nil {
			return nil
		}

		if _, hasZap := resolveImportAlias(file, zapImportPath); hasZap {
			dirHasZap[filepath.Dir(path)] = true
		}
		files = append(files, parsedGoFile{Path: path, File: file, Fset: fset})
		return nil
	})
	if err != nil {
		return nil, err
	}

	var violations []violation
	for _, pf := range files {
		violations = append(violations, lintParsedFile(pf.Path, pf.File, pf.Fset, dirHasZap[filepath.Dir(pf.Path)])...)
	}

	return violations, nil
}

// parseGoFile parses one Go source file, returning a nil *ast.File for a
// file every check skips: a ".pb.go" file, or one carrying the standard
// "Code generated ... DO NOT EDIT." marker.
func parseGoFile(path string, content []byte) (*ast.File, *token.FileSet, error) {
	if strings.HasSuffix(path, ".pb.go") {
		return nil, nil, nil
	}
	if gosrc.IsGenerated(content) {
		return nil, nil, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse file: %w", err)
	}
	return file, fset, nil
}

// lintGoFile parses one Go file and returns every violation found in it,
// applying whichever checks are in scope for that file.
//
// It treats the file's own zap import as its only zap eligibility signal,
// since it has no sibling files to consult; scan instead computes
// eligibility across a whole directory and calls lintParsedFile directly.
func lintGoFile(path string, content []byte) ([]violation, error) {
	file, fset, err := parseGoFile(path, content)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, nil
	}

	_, zapEligible := resolveImportAlias(file, zapImportPath)
	return lintParsedFile(path, file, fset, zapEligible), nil
}

// lintParsedFile applies every check in scope to an already-parsed Go file,
// given zapEligible — whether the zapmsg, zapkey, sugar, and loggerlast
// checks apply to it; see fileContext.ZapEligible.
func lintParsedFile(path string, file *ast.File, fset *token.FileSet, zapEligible bool) []violation {
	zapAlias, hasZap := resolveImportAlias(file, zapImportPath)
	if !hasZap {
		zapAlias = "zap"
	}
	contextAlias, hasContext := resolveImportAlias(file, contextImportPath)
	grpcAlias, hasGRPC := resolveImportAlias(file, grpcImportPath)

	fileCtx := &fileContext{
		Path:         path,
		Fset:         fset,
		IsTest:       strings.HasSuffix(path, "_test.go"),
		IsCgo:        importsC(file),
		ZapAlias:     zapAlias,
		HasZap:       hasZap,
		ZapEligible:  zapEligible,
		ContextAlias: contextAlias,
		HasContext:   hasContext,
		GRPCAlias:    grpcAlias,
		HasGRPC:      hasGRPC,
		PBAliases:    pbImportAliases(file),
	}

	var violations []violation

	if fileCtx.IsTest {
		violations = append(violations, checkTestPackage(file, fileCtx)...)
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			violations = append(violations, lintFuncDecl(d, fileCtx)...)
		case *ast.GenDecl:
			violations = append(violations, lintGenDecl(d, fileCtx)...)
		}
	}

	return violations
}

// lintFuncDecl applies every check scoped to a single function or method
// declaration.
//
// Most checks here apply equally inside a _test.go file, since the
// underlying convention has no reason to relax in test code: testctx
// (gated inside inspectBody) is restricted to test files, since it is the
// only check whose replacement (t.Context()) does not exist elsewhere.
// handlerblank is the opposite exemption, scoped to production methods
// only; see its own declaration for why. The logger and private checks are
// scoped to production, non-cgo code; see their own declarations for why.
func lintFuncDecl(decl *ast.FuncDecl, fileCtx *fileContext) []violation {
	var violations []violation

	if decl.Recv != nil {
		violations = append(violations, checkReceiver(decl, fileCtx)...)
	}

	name := enclosingName(decl)

	violations = append(violations, checkBareNew(decl, fileCtx)...)
	if decl.Recv != nil && !fileCtx.IsTest {
		violations = append(violations, checkHandlerBlank(decl, name, fileCtx)...)
	}
	if !fileCtx.IsTest {
		violations = append(violations, checkLoggerParam(decl, name, fileCtx)...)
	}

	exemptParams := map[string]bool{}
	if !fileCtx.IsTest && !fileCtx.IsCgo {
		exemptParams = sameTypeParamNames(decl)
		violations = append(violations, scanPrivateSelectors(decl, name, exemptParams, fileCtx)...)
	}

	violations = append(violations, inspectBody(decl, name, fileCtx)...)

	return violations
}

// lintGenDecl applies every check scoped to a package-level var, const, or
// type declaration.
func lintGenDecl(decl *ast.GenDecl, fileCtx *fileContext) []violation {
	var violations []violation

	switch decl.Tok {
	case token.VAR, token.CONST:
		for _, spec := range decl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || len(valueSpec.Names) == 0 {
				continue
			}
			name := valueSpec.Names[0].Name
			if !fileCtx.IsTest && !fileCtx.IsCgo {
				violations = append(violations, scanPrivateSelectors(valueSpec, name, nil, fileCtx)...)
			}
			violations = append(violations, inspectBody(valueSpec, name, fileCtx)...)
		}
	case token.TYPE:
		for _, spec := range decl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			violations = append(violations, checkLoggerLast(typeSpec, fileCtx)...)
			if !fileCtx.IsTest {
				violations = append(violations, checkLoggerInterface(typeSpec, fileCtx)...)
			}
			violations = append(violations, inspectBody(typeSpec, typeSpec.Name.Name, fileCtx)...)
		}
	}

	return violations
}

// inspectBody walks node and applies every check whose site is discovered
// by a generic AST traversal rather than by top-level declaration shape,
// attributing every finding to enclosing.
func inspectBody(node ast.Node, enclosing string, fileCtx *fileContext) []violation {
	var violations []violation

	ast.Inspect(node, func(n ast.Node) bool {
		switch expr := n.(type) {
		case *ast.ForStmt:
			violations = append(violations, checkLoopIndexFor(expr, enclosing, fileCtx)...)
		case *ast.RangeStmt:
			violations = append(violations, checkLoopIndexRange(expr, enclosing, fileCtx)...)
		case *ast.SelectorExpr:
			if fileCtx.ZapEligible {
				violations = append(violations, checkSugarType(expr, enclosing, fileCtx)...)
			}
		case *ast.CallExpr:
			violations = append(violations, checkMapLiteral(expr, enclosing, fileCtx)...)
			violations = append(violations, checkGRPCDial(expr, enclosing, fileCtx)...)
			if fileCtx.ZapEligible {
				violations = append(violations, checkSugarCall(expr, enclosing, fileCtx)...)
				violations = append(violations, checkZapMsg(expr, enclosing, fileCtx)...)
				violations = append(violations, checkZapKey(expr, enclosing, fileCtx)...)
			}
			if fileCtx.IsTest && fileCtx.HasContext {
				violations = append(violations, checkTestContext(expr, enclosing, fileCtx)...)
			}
		}
		return true
	})

	return violations
}

// importsC reports whether file imports the pseudo-package "C", marking it
// as CGO source whose private field names are C identifiers, not Go ones.
func importsC(file *ast.File) bool {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err == nil && path == "C" {
			return true
		}
	}
	return false
}

// enclosingName returns the display name of decl for use as the enclosing
// part of an allowlist key: "RecvType.MethodName" for a method, or the
// bare function name otherwise, which already covers a package-level init
// func.
func enclosingName(decl *ast.FuncDecl) string {
	if decl.Recv == nil {
		return decl.Name.Name
	}
	return receiverTypeName(decl.Recv) + "." + decl.Name.Name
}

// receiverTypeName returns the bare receiver type name for recv, stripping
// any pointer and generic-instantiation wrapping.
//
// For example, both "*Foo" and "*Foo[T]" return "Foo". It returns "" if
// recv has no receiver or the receiver's type is not a named type.
func receiverTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return bareTypeName(recv.List[0].Type)
}

// bareTypeName returns the bare type name denoted by expr, stripping any
// pointer and generic-instantiation wrapping.
//
// For example, "*Foo", "Foo[T]", and "*Foo[T]" all return "Foo". It
// returns "" for an expression that does not resolve to a named type, such
// as a qualified identifier or a struct type literal.
func bareTypeName(expr ast.Expr) string {
	for {
		switch t := expr.(type) {
		case *ast.StarExpr:
			expr = t.X
		case *ast.IndexExpr:
			expr = t.X
		case *ast.IndexListExpr:
			expr = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

// versionSegmentRe matches a semantic-import-versioning path segment, such
// as "v1" or "v2".
var versionSegmentRe = regexp.MustCompile(`^v[0-9]+$`)

// importLocalName returns the local package name Go infers for an unaliased
// import of path.
//
// It is ordinarily the last slash-separated segment, but this repository's
// generated protobuf packages live under a semantic-import-versioning
// segment (e.g. "modules/decap/controlplane/decappb/v1"), whose actual
// package name — set by the generated go_package option — is the segment
// before "v1", not "v1" itself. When the last segment matches that pattern,
// this function falls back to the segment before it instead.
func importLocalName(path string) string {
	segments := strings.Split(path, "/")
	last := segments[len(segments)-1]
	if len(segments) > 1 && versionSegmentRe.MatchString(last) {
		return segments[len(segments)-2]
	}
	return last
}

// resolveImportAlias returns the local identifier bound to the import of
// importPath in file, and whether that import is present at all.
//
// It honors a named import alias and otherwise falls back to
// importLocalName. That derivation matches the actual local package name for
// every import path this function is called with today (zap, context,
// grpc), but the two need not coincide in general — "gopkg.in/yaml.v3", for
// example, is package yaml, not yaml.v3 — so a caller adding a new import
// path here must confirm the match holds for it too.
func resolveImportAlias(file *ast.File, importPath string) (string, bool) {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		return importLocalName(path), true
	}
	return "", false
}

// pbImportAliases returns the set of file's local import identifiers whose
// name ends in "pb", the naming convention this repository's generated
// protobuf packages follow.
func pbImportAliases(file *ast.File) map[string]bool {
	aliases := map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}

		local := ""
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				continue
			}
			local = imp.Name.Name
		} else {
			local = importLocalName(path)
		}

		if strings.HasSuffix(local, "pb") {
			aliases[local] = true
		}
	}
	return aliases
}

// withOccurrenceOrdinals returns a copy of violations with every Key
// suffixed by a 1-based occurrence ordinal, counted among the violations
// sharing that same Key, in the order they appear in violations.
//
// Every check builds Key from the check name, the file path, the enclosing
// declaration, and the offending content, so several violations of one
// check inside one function — three "for i := range" loops in the same
// test, say — share an identical Key before this pass runs, and would
// otherwise all resolve to one allowlist row regardless of how many of them
// are actually fixed. Appending the ordinal turns each occurrence into its
// own row: a newly introduced occurrence gets a key no row covers yet, and
// paying down some but not all of them leaves the remaining rows reported
// as stale.
func withOccurrenceOrdinals(violations []violation) []violation {
	withOrdinals := make([]violation, len(violations))
	counts := map[string]int{}
	for idx, v := range violations {
		counts[v.Key]++
		v.Key = fmt.Sprintf("%s:%d", v.Key, counts[v.Key])
		withOrdinals[idx] = v
	}
	return withOrdinals
}

// report prints every unsuppressed violation, every allowlist issue, and
// every stale allowlist entry, and returns whether the run should fail.
func report(violations []violation, allowlistPath string) bool {
	allowlist, err := ledger.Load(allowlistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return true
	}

	failed := false

	for _, v := range withOccurrenceOrdinals(violations) {
		if allowlist.Suppresses(v.Key) {
			continue
		}
		fmt.Printf("%s:%d: %s\n", v.Path, v.Line, v.Message)
		failed = true
	}

	if allowlist.Report(os.Stdout) {
		failed = true
	}

	return failed
}
