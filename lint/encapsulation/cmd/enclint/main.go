// Command enclint enforces call-site encapsulation.
//
// A private field or method may only be reached through the bare receiver
// identifier m (rule 1), and every _test.go file must live in an external
// test package so it cannot reach into private fields or methods at all
// (rule 2). Known violations are tracked in two allowlists so that paying
// down the debt is enforced by deleting the corresponding row.
//
// A file that imports "C" is exempt from rule 1, since C struct fields are
// syntactically indistinguishable from Go private fields without type
// information. A method parameter declared with the same type as its
// receiver is exempt too, since an operation on another value of one's own
// type crosses no encapsulation boundary.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strconv"
	"strings"

	"github.com/yanet-platform/yanet2/lint/internal/gosrc"
	"github.com/yanet-platform/yanet2/lint/internal/ledger"
)

// privateViolation describes one selector expression that reaches into a
// private field or method from outside the receiver m.
type privateViolation struct {
	// Key is the stable allowlist key: "<path>:<enclosing>:<selector-text>".
	Key string
	// Path is the file path, relative to the repo root and slash-separated.
	Path string
	// Line is the 1-based source line of the selector expression.
	Line int
	// Text is the rendered selector expression, e.g. "m.opts.log".
	Text string
}

// testpkgViolation describes one _test.go file whose package clause is not
// an external test package.
type testpkgViolation struct {
	// Key is the stable allowlist key: "<path>:<pkgname>".
	Key string
	// Path is the file path, relative to the repo root and slash-separated.
	Path string
	// Line is the 1-based source line of the package clause.
	Line int
	// Pkg is the declared package name.
	Pkg string
}

func main() {
	var excludes gosrc.ExcludeList
	flag.Var(&excludes, "exclude", "directory to exclude (may be repeated)")
	privateAllowlistPath := flag.String("allowlist-private", "lint/encapsulation/allowlist-private.txt", "path to the rule 1 allowlist file")
	testpkgAllowlistPath := flag.String("allowlist-testpkg", "lint/encapsulation/allowlist-testpkg.txt", "path to the rule 2 allowlist file")
	flag.Parse()

	privateViolations, testpkgViolations, err := scan(".", excludes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error scanning: %v\n", err)
		os.Exit(1)
	}

	if report(privateViolations, testpkgViolations, *privateAllowlistPath, *testpkgAllowlistPath) {
		os.Exit(1)
	}
}

// scan walks the directory tree rooted at root and returns every rule 1 and
// rule 2 violation found in non-excluded Go files.
func scan(root string, excludes gosrc.ExcludeList) ([]privateViolation, []testpkgViolation, error) {
	var privateViolations []privateViolation
	var testpkgViolations []testpkgViolation

	err := gosrc.Walk(root, excludes, func(path string) error {
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		fileViolations, fileTestpkgViolations, err := lintGoFile(path, content)
		if err != nil {
			return fmt.Errorf("failed to lint %s: %w", path, err)
		}
		privateViolations = append(privateViolations, fileViolations...)
		testpkgViolations = append(testpkgViolations, fileTestpkgViolations...)
		return nil
	})

	return privateViolations, testpkgViolations, err
}

// lintGoFile parses one Go file and applies whichever rule applies to it:
// rule 2 for a _test.go file, rule 1 otherwise.
func lintGoFile(path string, content []byte) ([]privateViolation, []testpkgViolation, error) {
	if gosrc.IsGenerated(content) {
		return nil, nil, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse file: %w", err)
	}

	if strings.HasSuffix(path, "_test.go") {
		v, ok := checkTestPackage(path, file, fset)
		if !ok {
			return nil, nil, nil
		}
		return nil, []testpkgViolation{v}, nil
	}

	if strings.HasSuffix(path, ".pb.go") {
		return nil, nil, nil
	}

	if importsC(file) {
		return nil, nil, nil
	}

	return checkPrivateAccess(path, file, fset), nil, nil
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

// checkTestPackage reports a rule 2 violation when file's package clause
// does not end in "_test".
//
// A package main is also clean: a main package is unimportable, so an
// external test package for it could never reach anything, making
// "package main" the only possible clause for a command's test.
func checkTestPackage(path string, file *ast.File, fset *token.FileSet) (testpkgViolation, bool) {
	pkg := file.Name.Name
	if strings.HasSuffix(pkg, "_test") || pkg == "main" {
		return testpkgViolation{}, false
	}

	return testpkgViolation{
		Key:  path + ":" + pkg,
		Path: path,
		Line: fset.Position(file.Package).Line,
		Pkg:  pkg,
	}, true
}

// checkPrivateAccess walks every top-level declaration in file and returns
// a rule 1 violation for each selector expression that reaches into a
// private field or method from outside the receiver m.
func checkPrivateAccess(path string, file *ast.File, fset *token.FileSet) []privateViolation {
	var violations []privateViolation

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			violations = append(violations, scanPrivateSelectors(d, path, funcEnclosingName(d), sameTypeParamNames(d), fset)...)
		case *ast.GenDecl:
			if d.Tok != token.VAR && d.Tok != token.CONST {
				continue
			}
			for _, spec := range d.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok || len(valueSpec.Names) == 0 {
					continue
				}
				enclosing := valueSpec.Names[0].Name
				violations = append(violations, scanPrivateSelectors(valueSpec, path, enclosing, nil, fset)...)
			}
		}
	}

	return violations
}

// scanPrivateSelectors walks node and returns a violation for every
// *ast.SelectorExpr whose selector is unexported and whose base is neither
// the bare identifier m nor a name in exemptParams.
func scanPrivateSelectors(node ast.Node, path, enclosing string, exemptParams map[string]bool, fset *token.FileSet) []privateViolation {
	var violations []privateViolation

	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ast.IsExported(sel.Sel.Name) {
			return true
		}
		if base, ok := sel.X.(*ast.Ident); ok && (base.Name == "m" || exemptParams[base.Name]) {
			return true
		}

		text := types.ExprString(sel)
		violations = append(violations, privateViolation{
			Key:  fmt.Sprintf("%s:%s:%s", path, enclosing, text),
			Path: path,
			Line: fset.Position(sel.Pos()).Line,
			Text: text,
		})
		return true
	})

	return violations
}

// funcEnclosingName returns the display name of decl for use as the
// enclosing part of an allowlist key: "RecvType.MethodName" for a method,
// or the bare function name otherwise, which already covers a package-level
// init func.
func funcEnclosingName(decl *ast.FuncDecl) string {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
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
// For example, "*Foo", "Foo[T]", and "*Foo[T]" all return "Foo". It returns
// "" for an expression that does not resolve to a named type, such as a
// qualified identifier or a struct type literal.
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

// sameTypeParamNames returns the set of decl's parameter names whose
// declared type is the same bare named type as decl's receiver, ignoring
// pointer and generic-instantiation wrapping.
//
// It returns an empty set for a decl with no receiver. This is a
// name-based approximation, like the rest of this file's AST-only
// analysis: the exempt set is computed once per FuncDecl and applied to
// the whole body, so a same-type parameter's name that gets shadowed
// anywhere inside it — by a local variable, or by a nested closure
// parameter of a different type — is still treated as exempt.
func sameTypeParamNames(decl *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}

	if decl.Recv == nil || len(decl.Recv.List) == 0 || decl.Type.Params == nil {
		return names
	}

	receiverType := receiverTypeName(decl.Recv)
	if receiverType == "" {
		return names
	}

	for _, field := range decl.Type.Params.List {
		if bareTypeName(field.Type) != receiverType {
			continue
		}
		for _, name := range field.Names {
			names[name.Name] = true
		}
	}

	return names
}

// report prints every unsuppressed violation from both rules, every
// allowlist issue, and every stale allowlist entry, and returns whether the
// run should fail.
func report(privateViolations []privateViolation, testpkgViolations []testpkgViolation, privateAllowlistPath, testpkgAllowlistPath string) bool {
	privateLedger, err := ledger.Load(privateAllowlistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return true
	}
	testpkgLedger, err := ledger.Load(testpkgAllowlistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return true
	}

	failed := false

	for _, v := range privateViolations {
		if privateLedger.Suppresses(v.Key) {
			continue
		}
		fmt.Printf("%s:%d: %s reaches into a private field or method outside the receiver m; give the type an exported method or field\n", v.Path, v.Line, v.Text)
		failed = true
	}

	for _, v := range testpkgViolations {
		if testpkgLedger.Suppresses(v.Key) {
			continue
		}
		fmt.Printf("%s:%d: internal test package %s; move the test to package %s_test so it cannot reach into private fields and methods\n", v.Path, v.Line, v.Pkg, v.Pkg)
		failed = true
	}

	if privateLedger.Report(os.Stdout) {
		failed = true
	}
	if testpkgLedger.Report(os.Stdout) {
		failed = true
	}

	return failed
}
