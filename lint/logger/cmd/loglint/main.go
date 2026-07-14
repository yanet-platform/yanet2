// Command loglint enforces the options pattern for loggers.
//
// It walks the Go source tree and rejects any constructor or method whose
// parameter list carries a go.uber.org/zap logger, since constructors and
// methods accepting a logger directly must instead accept it through the
// options pattern (WithLog). Known violations are tracked in an allowlist so
// that paying down debt is enforced by deleting the corresponding row.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// zapImportPath is the import path of the logging package whose Logger and
// SugaredLogger types must not be accepted directly by constructors or
// methods.
const zapImportPath = "go.uber.org/zap"

// constructorNameRe matches constructor function names, e.g. NewACLModule or
// newRIBStore, while excluding names that merely start with "new"/"New" as a
// word fragment, such as NewsFeed or newlineSplitter.
var constructorNameRe = regexp.MustCompile(`^[Nn]ew([A-Z_]|$)`)

// generatedRe matches the standard "generated code" marker comment.
var generatedRe = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// nodeModulesName is the node_modules directory basename, skipped wherever
// it appears in the tree since JavaScript tooling nests it arbitrarily
// deeply (e.g. web/node_modules).
const nodeModulesName = "node_modules"

// rootRelativeExcludes are directories skipped by their path relative to
// the scan root, rather than by basename, so that a same-named directory
// elsewhere in the tree is still scanned.
//
// "tests" is the top-level QEMU functional-test harness, deliberately out
// of scope for this rule per an explicit project decision: it holds several
// *zap.SugaredLogger constructors and methods that are intentionally not
// ledgered. "subprojects" holds the DPDK meson subproject. The "build*"
// entries are the meson build directories created by "meson setup"; they
// are matched exactly, not by prefix, so that a future Go package such as
// "builder/" or "buildinfo/" is not silently exempted.
var rootRelativeExcludes = map[string]bool{
	"tests":       true,
	"subprojects": true,
	"build":       true,
	"build-asan":  true,
	"build-tsan":  true,
	"build-perf":  true,
}

// excludeList is a flag.Value that accumulates multiple --exclude values.
type excludeList []string

func (e *excludeList) String() string {
	return strings.Join(*e, ", ")
}

func (e *excludeList) Set(v string) error {
	*e = append(*e, filepath.Clean(v))
	return nil
}

// violation describes one checked function or method that takes a logger.
type violation struct {
	// Key is the stable allowlist key: "<path>:<Name>".
	Key string
	// Path is the file path, relative to the repo root and slash-separated.
	Path string
	// Line is the 1-based source line of the declaration.
	Line int
	// Name is the display name: "FuncName" or "RecvType.MethodName".
	Name string
}

func main() {
	var excludes excludeList
	flag.Var(&excludes, "exclude", "directory to exclude (may be repeated)")
	allowlistPath := flag.String("allowlist", "lint/logger/allowlist.txt", "path to the allowlist file")
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

// isExcluded reports whether path is inside any of the excluded directories.
func isExcluded(path string, excludes []string) bool {
	for _, ex := range excludes {
		// filepath.Rel returns a path without ".." prefix when path is inside
		// ex.
		rel, err := filepath.Rel(ex, path)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}

// scan walks the directory tree rooted at root and returns every violation
// found in non-excluded, non-generated, non-test Go files.
func scan(root string, excludes []string) ([]violation, error) {
	var violations []violation

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			name := info.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			if name == nodeModulesName {
				return filepath.SkipDir
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil && rootRelativeExcludes[rel] {
				return filepath.SkipDir
			}
			if isExcluded(path, excludes) {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".pb.go") {
			return nil
		}

		fileViolations, err := lintGoFile(path)
		if err != nil {
			return fmt.Errorf("failed to lint %s: %w", path, err)
		}
		violations = append(violations, fileViolations...)
		return nil
	})

	return violations, err
}

// lintGoFile parses one Go file and returns every violation found in it.
func lintGoFile(path string) ([]violation, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	if isGenerated(content) {
		return nil, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file: %w", err)
	}

	zapAlias, ok := resolveZapAlias(file)
	if !ok {
		return nil, nil
	}

	var violations []violation
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if v, ok := checkFuncDecl(d, zapAlias, path, fset); ok {
				violations = append(violations, v)
			}
		case *ast.GenDecl:
			violations = append(violations, checkGenDecl(d, zapAlias, path, fset)...)
		}
	}

	return violations, nil
}

// isGenerated reports whether content carries the standard "generated code"
// marker comment before its package clause.
func isGenerated(content []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if generatedRe.MatchString(line) {
			return true
		}
		if strings.HasPrefix(strings.TrimSpace(line), "package ") {
			return false
		}
	}
	return false
}

// resolveZapAlias returns the local identifier bound to the zap import in
// file, and whether zap is imported at all.
//
// It honors a named import alias.
func resolveZapAlias(file *ast.File) (string, bool) {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != zapImportPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		return "zap", true
	}
	return "", false
}

// checkFuncDecl reports a violation when decl is a constructor or a method
// and one of its parameters carries a zap logger.
func checkFuncDecl(decl *ast.FuncDecl, zapAlias, path string, fset *token.FileSet) (violation, bool) {
	isConstructor := decl.Recv == nil && constructorNameRe.MatchString(decl.Name.Name)
	isMethod := decl.Recv != nil
	if !isConstructor && !isMethod {
		return violation{}, false
	}

	if !paramsContainLogger(decl.Type.Params, zapAlias) {
		return violation{}, false
	}

	name := decl.Name.Name
	if isMethod {
		name = receiverTypeName(decl.Recv) + "." + decl.Name.Name
	}

	return violation{
		Key:  path + ":" + name,
		Path: path,
		Line: fset.Position(decl.Pos()).Line,
		Name: name,
	}, true
}

// checkGenDecl reports a violation for every interface method declaration in
// decl whose parameter list carries a zap logger.
//
// Non-interface type declarations produce no violations.
func checkGenDecl(decl *ast.GenDecl, zapAlias, path string, fset *token.FileSet) []violation {
	if decl.Tok != token.TYPE {
		return nil
	}

	var violations []violation
	for _, spec := range decl.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		iface, ok := typeSpec.Type.(*ast.InterfaceType)
		if !ok || iface.Methods == nil {
			continue
		}

		for _, field := range iface.Methods.List {
			funcType, ok := field.Type.(*ast.FuncType)
			if !ok || len(field.Names) == 0 {
				continue
			}
			if !paramsContainLogger(funcType.Params, zapAlias) {
				continue
			}

			for _, methodName := range field.Names {
				name := typeSpec.Name.Name + "." + methodName.Name
				violations = append(violations, violation{
					Key:  path + ":" + name,
					Path: path,
					Line: fset.Position(field.Pos()).Line,
					Name: name,
				})
			}
		}
	}

	return violations
}

// receiverTypeName returns the bare receiver type name for recv, stripping
// any pointer and generic-instantiation wrapping.
//
// For example, both "*Foo" and "*Foo[T]" return "Foo".
func receiverTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}

	expr := recv.List[0].Type
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

// paramsContainLogger reports whether any parameter type in params contains
// a zap.Logger or zap.SugaredLogger reference, however deeply nested (behind
// a pointer, slice, variadic, or function-type wrapping).
func paramsContainLogger(params *ast.FieldList, zapAlias string) bool {
	if params == nil {
		return false
	}

	for _, field := range params.List {
		found := false
		ast.Inspect(field.Type, func(n ast.Node) bool {
			if found {
				return false
			}
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if ok && ident.Name == zapAlias && (sel.Sel.Name == "Logger" || sel.Sel.Name == "SugaredLogger") {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}

	return false
}

// allowlistEntry is one parsed, well-formed row of the allowlist file.
type allowlistEntry struct {
	Line   int
	Reason string
}

// allowlistIssue is a malformed or reasonless allowlist row, reported as a
// linter failure in its own right.
type allowlistIssue struct {
	Line    int
	Message string
}

// parseAllowlist reads the allowlist file at path and returns its
// well-formed entries keyed by violation key, plus any malformed or
// reasonless rows found along the way.
//
// A missing file is not an error — it is treated as an empty allowlist.
func parseAllowlist(path string) (map[string]allowlistEntry, []allowlistIssue, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]allowlistEntry{}, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to open allowlist: %w", err)
	}
	defer file.Close()

	entries := map[string]allowlistEntry{}
	var issues []allowlistIssue

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		key := trimmed
		reason := ""
		if before, after, found := strings.Cut(trimmed, "#"); found {
			key = strings.TrimSpace(before)
			reason = strings.TrimSpace(after)
		}

		if !strings.Contains(key, ":") {
			issues = append(issues, allowlistIssue{
				Line:    lineNumber,
				Message: fmt.Sprintf("malformed entry %q, expected \"<path>:<name>  # <reason>\"", key),
			})
			continue
		}
		if reason == "" {
			issues = append(issues, allowlistIssue{
				Line:    lineNumber,
				Message: fmt.Sprintf("entry %s is missing a mandatory reason, add \"# <reason>\"", key),
			})
			continue
		}
		if existing, ok := entries[key]; ok {
			issues = append(issues, allowlistIssue{
				Line:    lineNumber,
				Message: fmt.Sprintf("duplicate entry %s, already defined on line %d", key, existing.Line),
			})
			continue
		}

		entries[key] = allowlistEntry{Line: lineNumber, Reason: reason}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to read allowlist: %w", err)
	}

	return entries, issues, nil
}

// report prints every unsuppressed violation, every allowlist issue, and
// every stale allowlist entry, and returns whether the run should fail.
func report(violations []violation, allowlistPath string) bool {
	entries, issues, err := parseAllowlist(allowlistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return true
	}

	failed := false

	matched := map[string]bool{}
	for _, v := range violations {
		if _, ok := entries[v.Key]; ok {
			matched[v.Key] = true
			continue
		}
		fmt.Printf("%s:%d: %s must not take a logger as a parameter; pass it through the options pattern (WithLog)\n", v.Path, v.Line, v.Name)
		failed = true
	}

	for _, issue := range issues {
		fmt.Printf("%s:%d: %s\n", allowlistPath, issue.Line, issue.Message)
		failed = true
	}

	var staleKeys []string
	for key := range entries {
		if !matched[key] {
			staleKeys = append(staleKeys, key)
		}
	}
	sort.Slice(staleKeys, func(i, j int) bool {
		return entries[staleKeys[i]].Line < entries[staleKeys[j]].Line
	})
	for _, key := range staleKeys {
		fmt.Printf("%s:%d: stale entry %s — no such violation; remove it\n", allowlistPath, entries[key].Line, key)
		failed = true
	}

	return failed
}
