package main

import (
	"fmt"
	"go/ast"
	"regexp"
)

// constructorNameRe matches constructor function names, e.g. NewACLModule
// or newRIBStore, while excluding names that merely start with "new"/"New"
// as a word fragment, such as NewsFeed or newlineSplitter.
var constructorNameRe = regexp.MustCompile(`^[Nn]ew([A-Z_]|$)`)

// checkLoggerParam reports a violation when decl is a constructor or a
// method and one of its parameters carries a zap logger.
//
// A logger accepted positionally ties every call site to that exact
// parameter list. The options pattern (WithLog) lets a constructor or method
// gain further optional dependencies later without breaking existing
// callers. Scope: production code only. A _test.go file is skipped entirely
// by its caller, lintFuncDecl, since the options pattern this check enforces
// has no test-code exemption to preserve — the check simply never fires
// there in practice, so scoping it out keeps a hand-rolled test double from
// needing an allowlist row.
func checkLoggerParam(decl *ast.FuncDecl, name string, fileCtx *fileContext) []violation {
	if !fileCtx.HasZap {
		return nil
	}

	isConstructor := decl.Recv == nil && constructorNameRe.MatchString(decl.Name.Name)
	isMethod := decl.Recv != nil
	if !isConstructor && !isMethod {
		return nil
	}

	if !paramsContainLogger(decl.Type.Params, fileCtx.ZapAlias) {
		return nil
	}

	return []violation{{
		Check:   "logger",
		Key:     fmt.Sprintf("logger:%s:%s", fileCtx.Path, name),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(decl.Pos()).Line,
		Message: fmt.Sprintf("%s must not take a logger as a parameter; pass it through the options pattern (WithLog)", name),
	}}
}

// checkLoggerInterface reports a violation for every interface method
// declaration in typeSpec whose parameter list carries a zap logger.
//
// It flags the same anti-pattern as checkLoggerParam, for the reason given
// there.
//
// Scope: production code only, gated by its caller lintGenDecl on
// !fileCtx.IsTest, on the same scoping grounds as checkLoggerParam's own
// test-file exemption. Non-interface type declarations produce no
// violations.
func checkLoggerInterface(typeSpec *ast.TypeSpec, fileCtx *fileContext) []violation {
	if !fileCtx.HasZap {
		return nil
	}

	iface, ok := typeSpec.Type.(*ast.InterfaceType)
	if !ok || iface.Methods == nil {
		return nil
	}

	var violations []violation
	for _, field := range iface.Methods.List {
		funcType, ok := field.Type.(*ast.FuncType)
		if !ok || len(field.Names) == 0 {
			continue
		}
		if !paramsContainLogger(funcType.Params, fileCtx.ZapAlias) {
			continue
		}

		for _, methodName := range field.Names {
			name := typeSpec.Name.Name + "." + methodName.Name
			violations = append(violations, violation{
				Check:   "logger",
				Key:     fmt.Sprintf("logger:%s:%s", fileCtx.Path, name),
				Path:    fileCtx.Path,
				Line:    fileCtx.Fset.Position(field.Pos()).Line,
				Message: fmt.Sprintf("%s must not take a logger as a parameter; pass it through the options pattern (WithLog)", name),
			})
		}
	}

	return violations
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
