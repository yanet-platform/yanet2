package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"
)

// checkTestPackage reports a violation when file's package clause is a
// _test.go file whose declared package is neither an external test package
// nor "main".
//
// An internal test package can reach a type's private fields and methods
// directly, defeating the guarantee that outside code only crosses a
// type's exported surface. Scope: _test.go files only, gated by its caller
// lintParsedFile. A package main is clean: a main package is unimportable,
// so an external test package for it could never reach anything, making
// "package main" the only possible clause for a command's test.
func checkTestPackage(file *ast.File, fileCtx *fileContext) []violation {
	pkg := file.Name.Name
	if strings.HasSuffix(pkg, "_test") || pkg == "main" {
		return nil
	}

	return []violation{{
		Check:   "testpkg",
		Key:     fmt.Sprintf("testpkg:%s:%s", fileCtx.Path, pkg),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(file.Package).Line,
		Message: fmt.Sprintf("internal test package %s; move the test to package %s_test so it cannot reach into private fields and methods", pkg, pkg),
	}}
}

// scanPrivateSelectors walks node and returns a violation for every
// *ast.SelectorExpr whose selector is unexported and whose base is neither
// the bare identifier m nor a name in exemptParams.
//
// Reaching a private field or method through anything but the receiver m
// lets code elsewhere in the package bypass whatever invariant or mutex
// the owning type's own methods maintain over it. Scope: production,
// non-cgo, non-test code, gated by its callers lintFuncDecl and
// lintGenDecl. A file that imports "C" is exempt, since C struct fields
// are syntactically indistinguishable from Go private fields without type
// information. A _test.go file is exempt too: it is checked by
// checkTestPackage instead, which enforces the external-test-package rule
// that keeps a test file from reaching into private fields at all.
func scanPrivateSelectors(node ast.Node, enclosing string, exemptParams map[string]bool, fileCtx *fileContext) []violation {
	var violations []violation

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
		violations = append(violations, violation{
			Check:   "private",
			Key:     fmt.Sprintf("private:%s:%s:%s", fileCtx.Path, enclosing, text),
			Path:    fileCtx.Path,
			Line:    fileCtx.Fset.Position(sel.Pos()).Line,
			Message: fmt.Sprintf("%s reaches into a private field or method outside the receiver m; give the type an exported method or field", text),
		})
		return true
	})

	return violations
}

// sameTypeParamNames returns the set of decl's parameter names whose
// declared type is the same bare named type as decl's receiver, ignoring
// pointer and generic-instantiation wrapping.
//
// It returns an empty set for a decl with no receiver. This is a
// name-based approximation, like the rest of this check's AST-only
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
