package main

import (
	"fmt"
	"go/ast"
)

// checkTestContext reports a violation when call is context.Background()
// or context.TODO(), inside a _test.go file.
//
// t.Context() is canceled automatically when the test finishes, whereas
// context.Background() and context.TODO() never cancel, so a value built
// from either can leak a goroutine or resource past its owning test.
// Scope: _test.go files only, gated by its caller inspectBody, since
// t.Context() — the replacement this check demands — does not exist
// outside of test code.
func checkTestContext(call *ast.CallExpr, enclosing string, fileCtx *fileContext) []violation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != fileCtx.ContextAlias {
		return nil
	}
	if sel.Sel.Name != "Background" && sel.Sel.Name != "TODO" {
		return nil
	}

	name := enclosing + ":" + sel.Sel.Name
	return []violation{{
		Check:   "testctx",
		Key:     fmt.Sprintf("testctx:%s:%s", fileCtx.Path, name),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(call.Pos()).Line,
		Message: fmt.Sprintf("%s must use t.Context() instead of context.%s", enclosing, sel.Sel.Name),
	}}
}

// checkHandlerBlank reports a violation for every parameter of decl, named
// name, whose type is context.Context or a "pb"-suffixed package type and
// whose name is the blank identifier.
//
// A named ctx or request parameter documents the handler's signature and
// stays available for a later change to start reading it. The blank
// identifier discards that name and forces a signature edit before the
// value can be used. Scope: production methods only, gated by its caller
// lintFuncDecl on decl.Recv != nil and !fileCtx.IsTest — a free function is
// never checked. A test double implementing an interface routinely blanks
// out every parameter it does not need, so this check does not apply to
// test code.
func checkHandlerBlank(decl *ast.FuncDecl, name string, fileCtx *fileContext) []violation {
	if decl.Type.Params == nil {
		return nil
	}

	var violations []violation
	for _, field := range decl.Type.Params.List {
		category := paramCategory(field.Type, fileCtx)
		if category == "" {
			continue
		}
		for _, paramName := range field.Names {
			if paramName.Name != "_" {
				continue
			}
			violations = append(violations, violation{
				Check:   "handlerblank",
				Key:     fmt.Sprintf("handlerblank:%s:%s:%s", fileCtx.Path, name, category),
				Path:    fileCtx.Path,
				Line:    fileCtx.Fset.Position(paramName.Pos()).Line,
				Message: fmt.Sprintf("%s parameter of type %s must not be named _", name, category),
			})
		}
	}
	return violations
}

// paramCategory returns the display type name of fieldType when it is
// context.Context or a type from a "pb"-suffixed imported package,
// stripping one leading pointer. It returns "" for any other type.
func paramCategory(fieldType ast.Expr, fileCtx *fileContext) string {
	expr := fieldType
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	if fileCtx.HasContext && ident.Name == fileCtx.ContextAlias && sel.Sel.Name == "Context" {
		return "context.Context"
	}
	if fileCtx.PBAliases[ident.Name] {
		return ident.Name + "." + sel.Sel.Name
	}
	return ""
}
