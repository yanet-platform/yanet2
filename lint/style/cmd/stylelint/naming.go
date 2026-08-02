package main

import (
	"fmt"
	"go/ast"
	"go/token"
)

// checkReceiver reports a violation when decl's method receiver has an
// explicit name other than m, including the blank identifier.
//
// A fixed receiver name lets the private check treat the bare identifier m
// as a method's own access path without per-type configuration. Scope:
// every method, in production and test code alike. An unnamed receiver,
// such as the stateless "noop" observer pattern (func (noopObserver)
// OnEvent()), is not flagged: it deliberately has no identifier to
// rename, unlike a receiver given some other name.
func checkReceiver(decl *ast.FuncDecl, fileCtx *fileContext) []violation {
	if len(decl.Recv.List[0].Names) == 0 {
		return nil
	}

	recvName := decl.Recv.List[0].Names[0].Name
	if recvName == "m" {
		return nil
	}

	name := receiverTypeName(decl.Recv) + "." + decl.Name.Name
	return []violation{{
		Check:   "receiver",
		Key:     fmt.Sprintf("receiver:%s:%s", fileCtx.Path, name),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(decl.Pos()).Line,
		Message: fmt.Sprintf("%s receiver must be named m, not %s", name, recvName),
	}}
}

// checkBareNew reports a violation when decl is a top-level function
// literally named New.
//
// A constructor literally named New stops naming the type it builds once
// a package needs a second constructible type. Scope: package-level
// functions only, in production and test code alike. A method literally
// named New is not a package's discoverable constructor, so it is out of
// scope.
func checkBareNew(decl *ast.FuncDecl, fileCtx *fileContext) []violation {
	if decl.Recv != nil || decl.Name.Name != "New" {
		return nil
	}

	return []violation{{
		Check:   "barenew",
		Key:     fmt.Sprintf("barenew:%s:New", fileCtx.Path),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(decl.Pos()).Line,
		Message: "constructor must not be named bare New; use a descriptive name like NewFoo",
	}}
}

// checkLoggerLast reports a violation for every *zap.Logger field of
// typeSpec that is not the last field in its struct, whether named or
// anonymously embedded.
//
// This is house style: keeping the logger last groups a struct's domain
// fields together ahead of its infrastructure plumbing. Scope: every
// struct type declaration, in production and test code alike, in any file
// whose package (not necessarily that file itself) imports zap.
func checkLoggerLast(typeSpec *ast.TypeSpec, fileCtx *fileContext) []violation {
	if !fileCtx.ZapEligible {
		return nil
	}
	structType, ok := typeSpec.Type.(*ast.StructType)
	if !ok || structType.Fields == nil {
		return nil
	}

	fields := structType.Fields.List
	var violations []violation
	for idx, field := range fields {
		if idx == len(fields)-1 {
			continue
		}
		if !isZapLoggerType(field.Type, fileCtx.ZapAlias) {
			continue
		}
		if len(field.Names) == 0 {
			name := typeSpec.Name.Name + ".Logger"
			violations = append(violations, violation{
				Check:   "loggerlast",
				Key:     fmt.Sprintf("loggerlast:%s:%s", fileCtx.Path, name),
				Path:    fileCtx.Path,
				Line:    fileCtx.Fset.Position(field.Pos()).Line,
				Message: fmt.Sprintf("%s embedded *zap.Logger field must be the last field in the struct", name),
			})
			continue
		}
		for _, fieldName := range field.Names {
			name := typeSpec.Name.Name + "." + fieldName.Name
			violations = append(violations, violation{
				Check:   "loggerlast",
				Key:     fmt.Sprintf("loggerlast:%s:%s", fileCtx.Path, name),
				Path:    fileCtx.Path,
				Line:    fileCtx.Fset.Position(field.Pos()).Line,
				Message: fmt.Sprintf("%s *zap.Logger field must be the last field in the struct", name),
			})
		}
	}
	return violations
}

// isZapLoggerType reports whether expr denotes *zap.Logger under zapAlias.
func isZapLoggerType(expr ast.Expr, zapAlias string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == zapAlias && sel.Sel.Name == "Logger"
}

// checkLoopIndexFor reports a violation when stmt's Init clause declares or
// assigns a loop variable named i.
//
// The repository's no-abbreviated-identifiers rule carves out idx, not i,
// as its one allowed loop-counter abbreviation, so a lone i reads as an
// unexplained shorthand next to every other spelled-out name. Scope:
// every for-loop, in production and test code alike. Both a
// short-variable-declaration Init clause (for i := 0; ...) and a plain
// assignment to a variable declared earlier (var i int; for i = 0; ...) are
// in scope, since the loop reads as an "i" index either way.
func checkLoopIndexFor(stmt *ast.ForStmt, enclosing string, fileCtx *fileContext) []violation {
	assign, ok := stmt.Init.(*ast.AssignStmt)
	if !ok || (assign.Tok != token.DEFINE && assign.Tok != token.ASSIGN) {
		return nil
	}
	for _, lhs := range assign.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if ok && ident.Name == "i" {
			return []violation{loopIndexViolation(ident.Pos(), enclosing, fileCtx)}
		}
	}
	return nil
}

// checkLoopIndexRange reports a violation when stmt's range key is named i.
//
// It flags the same abbreviated-identifier smell as checkLoopIndexFor, for
// the reason given there.
//
// Scope: every range loop, in production and test code alike.
func checkLoopIndexRange(stmt *ast.RangeStmt, enclosing string, fileCtx *fileContext) []violation {
	ident, ok := stmt.Key.(*ast.Ident)
	if !ok || ident.Name != "i" {
		return nil
	}
	return []violation{loopIndexViolation(ident.Pos(), enclosing, fileCtx)}
}

// loopIndexViolation builds the shared loopindex violation for a loop
// index found at pos, inside enclosing.
func loopIndexViolation(pos token.Pos, enclosing string, fileCtx *fileContext) violation {
	name := enclosing + ":i"
	return violation{
		Check:   "loopindex",
		Key:     fmt.Sprintf("loopindex:%s:%s", fileCtx.Path, name),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(pos).Line,
		Message: fmt.Sprintf("%s loop index must be idx, not i", enclosing),
	}
}
