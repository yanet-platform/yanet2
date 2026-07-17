// Command loglint enforces the options pattern for loggers.
//
// It walks the Go source tree and rejects any constructor or method whose
// parameter list carries a go.uber.org/zap logger, since constructors and
// methods accepting a logger directly must instead accept it through the
// options pattern (WithLog). Known violations are tracked in an allowlist so
// that paying down debt is enforced by deleting the corresponding row.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/yanet-platform/yanet2/lint/internal/gosrc"
	"github.com/yanet-platform/yanet2/lint/internal/ledger"
)

// zapImportPath is the import path of the logging package whose Logger and
// SugaredLogger types must not be accepted directly by constructors or
// methods.
const zapImportPath = "go.uber.org/zap"

// constructorNameRe matches constructor function names, e.g. NewACLModule or
// newRIBStore, while excluding names that merely start with "new"/"New" as a
// word fragment, such as NewsFeed or newlineSplitter.
var constructorNameRe = regexp.MustCompile(`^[Nn]ew([A-Z_]|$)`)

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
	var excludes gosrc.ExcludeList
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

// scan walks the directory tree rooted at root and returns every violation
// found in non-excluded, non-generated, non-test Go files.
func scan(root string, excludes gosrc.ExcludeList) ([]violation, error) {
	var violations []violation

	err := gosrc.Walk(root, excludes, func(path string) error {
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

	if gosrc.IsGenerated(content) {
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

// report prints every unsuppressed violation, every allowlist issue, and
// every stale allowlist entry, and returns whether the run should fail.
func report(violations []violation, allowlistPath string) bool {
	allowlist, err := ledger.Load(allowlistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return true
	}

	failed := false

	for _, v := range violations {
		if allowlist.Suppresses(v.Key) {
			continue
		}
		fmt.Printf("%s:%d: %s must not take a logger as a parameter; pass it through the options pattern (WithLog)\n", v.Path, v.Line, v.Name)
		failed = true
	}

	if allowlist.Report(os.Stdout) {
		failed = true
	}

	return failed
}
