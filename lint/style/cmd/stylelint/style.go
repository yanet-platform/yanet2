package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// loggerMethodNames are the *zap.Logger methods whose first argument is a
// log message.
var loggerMethodNames = map[string]bool{
	"Debug":  true,
	"Info":   true,
	"Warn":   true,
	"Error":  true,
	"Fatal":  true,
	"Panic":  true,
	"DPanic": true,
}

// snakeCaseRe matches a lowercase snake_case identifier.
var snakeCaseRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// checkMapLiteral reports a violation when call is a single-argument
// make(map[K]V), which a capacity-hinted two-argument make is not.
//
// This is house style: map[K]V{} reads the same as a struct or slice
// composite literal, instead of reaching for make when no capacity hint is
// being given. Scope: every call expression, in production and test code
// alike.
func checkMapLiteral(call *ast.CallExpr, enclosing string, fileCtx *fileContext) []violation {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "make" || len(call.Args) != 1 {
		return nil
	}
	mapType, ok := call.Args[0].(*ast.MapType)
	if !ok {
		return nil
	}

	rendered := types.ExprString(mapType)
	name := enclosing + ":" + rendered
	return []violation{{
		Check:   "maplit",
		Key:     fmt.Sprintf("maplit:%s:%s", fileCtx.Path, name),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(call.Pos()).Line,
		Message: fmt.Sprintf("%s use %s{} instead of make(%s)", enclosing, rendered, rendered),
	}}
}

// checkGRPCDial reports a violation when call is grpc.Dial or
// grpc.DialContext.
//
// grpc.Dial and grpc.DialContext are deprecated upstream in favor of
// grpc.NewClient. Scope: every call expression, in production and test
// code alike.
func checkGRPCDial(call *ast.CallExpr, enclosing string, fileCtx *fileContext) []violation {
	if !fileCtx.HasGRPC {
		return nil
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != fileCtx.GRPCAlias {
		return nil
	}
	if sel.Sel.Name != "Dial" && sel.Sel.Name != "DialContext" {
		return nil
	}

	name := enclosing + ":" + sel.Sel.Name
	return []violation{{
		Check:   "grpcdial",
		Key:     fmt.Sprintf("grpcdial:%s:%s", fileCtx.Path, name),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(call.Pos()).Line,
		Message: fmt.Sprintf("%s must use grpc.NewClient instead of grpc.%s", enclosing, sel.Sel.Name),
	}}
}

// checkSugarType reports a violation when sel denotes the zap.SugaredLogger
// type.
//
// zap.SugaredLogger accepts untyped key-value pairs checked only at runtime.
// *zap.Logger's typed field constructors (zap.String, zap.Int, ...) catch a
// mismatched argument at compile time instead. Scope: every selector
// expression, in production and test code alike, in any file whose package
// (not necessarily that file itself) imports zap. In practice this only ever
// fires in a file that itself imports zap, since the zap.SugaredLogger
// reference this check matches requires that file's own zap import to
// compile.
func checkSugarType(sel *ast.SelectorExpr, enclosing string, fileCtx *fileContext) []violation {
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != fileCtx.ZapAlias || sel.Sel.Name != "SugaredLogger" {
		return nil
	}

	name := enclosing + ":SugaredLogger"
	return []violation{{
		Check:   "sugar",
		Key:     fmt.Sprintf("sugar:%s:%s", fileCtx.Path, name),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(sel.Pos()).Line,
		Message: fmt.Sprintf("%s must use *zap.Logger instead of zap.SugaredLogger", enclosing),
	}}
}

// checkSugarCall reports a violation when call is a niladic .Sugar() call.
//
// It flags the same anti-pattern as checkSugarType, for the reason given
// there.
//
// Scope: every call expression, in production and test code alike, in any
// file whose package (not necessarily that file itself) imports zap.
func checkSugarCall(call *ast.CallExpr, enclosing string, fileCtx *fileContext) []violation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sugar" || len(call.Args) != 0 {
		return nil
	}

	name := enclosing + ":Sugar()"
	return []violation{{
		Check:   "sugar",
		Key:     fmt.Sprintf("sugar:%s:%s", fileCtx.Path, name),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(call.Pos()).Line,
		Message: fmt.Sprintf("%s must use *zap.Logger instead of calling .Sugar()", enclosing),
	}}
}

// checkZapMsg reports a violation when call is a logger call whose first
// argument is a string literal starting with an uppercase letter.
//
// A lowercase message follows the same convention as a Go error string, so
// it composes predictably when embedded inside a larger line. Scope: every
// call expression, in production and test code alike, in any file whose
// package (not necessarily that file itself) imports zap. A call on the
// bare identifier t or b is skipped: testing.T and testing.B expose Error
// and Fatal under the same names as the zap logger API, and their arguments
// are failure messages read by a human running the test, not structured log
// messages.
func checkZapMsg(call *ast.CallExpr, enclosing string, fileCtx *fileContext) []violation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !loggerMethodNames[sel.Sel.Name] || len(call.Args) == 0 {
		return nil
	}
	if base, ok := sel.X.(*ast.Ident); ok && (base.Name == "t" || base.Name == "b") {
		return nil
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil
	}
	message, err := strconv.Unquote(lit.Value)
	if err != nil || message == "" {
		return nil
	}
	firstRune, _ := utf8.DecodeRuneInString(message)
	if !unicode.IsUpper(firstRune) {
		return nil
	}

	name := enclosing + ":" + sel.Sel.Name + ":" + message
	return []violation{{
		Check:   "zapmsg",
		Key:     fmt.Sprintf("zapmsg:%s:%s", fileCtx.Path, name),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(lit.Pos()).Line,
		Message: fmt.Sprintf("%s log message %q must not start with an uppercase letter", enclosing, message),
	}}
}

// checkZapKey reports a violation when call is a zap.<Type> field
// constructor whose first argument is a string literal that is not
// snake_case.
//
// This is house style: a uniform snake_case key vocabulary keeps every
// structured log field queryable the same way, instead of mixing
// camelCase and snake_case across call sites. Scope: every call
// expression, in production and test code alike, in any file whose
// package (not necessarily that file itself) imports zap. In practice
// this only ever fires in a file that itself imports zap, since the
// zap.<Type>(...) syntax this check matches requires that file's own zap
// import to compile.
func checkZapKey(call *ast.CallExpr, enclosing string, fileCtx *fileContext) []violation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != fileCtx.ZapAlias || !ast.IsExported(sel.Sel.Name) {
		return nil
	}
	if len(call.Args) == 0 {
		return nil
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil
	}
	key, err := strconv.Unquote(lit.Value)
	if err != nil || snakeCaseRe.MatchString(key) {
		return nil
	}

	name := enclosing + ":" + key
	return []violation{{
		Check:   "zapkey",
		Key:     fmt.Sprintf("zapkey:%s:%s", fileCtx.Path, name),
		Path:    fileCtx.Path,
		Line:    fileCtx.Fset.Position(lit.Pos()).Line,
		Message: fmt.Sprintf("%s zap field key %q must be snake_case", enclosing, key),
	}}
}
