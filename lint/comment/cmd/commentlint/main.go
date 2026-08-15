// Command commentlint checks comment style in tracked files.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	gobuild "go/build"
	"go/build/constraint"
	"go/parser"
	"go/scanner"
	"go/token"
	"io"
	"maps"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type profile int

const (
	profileNone profile = iota
	profileSlash
	profileHash
	profileMarkup
	profileSQL
	profileINI
)

type finding struct {
	Path    string
	Line    int
	Message string
}

type trackedFile struct {
	Path string
	Mode string
	Hash string
}

type gitCommand func(root string, arguments ...string) *exec.Cmd

type shellHeredoc struct {
	Marker    string
	StripTabs bool
}

type shellLexState struct {
	Quote                      byte
	CommandDepth               int
	ResumeDoubleDepths         []int
	InWord                     bool
	WordContinues              bool
	BacktickDepth              int
	BacktickResumeDoubleDepths []int
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	findings, err := scan(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, finding := range findings {
		message := finding.Message
		if message == "" {
			message = "pure-run comment separator"
		}
		fmt.Printf("%s:%d: %s\n", finding.Path, finding.Line, message)
	}
	if len(findings) != 0 {
		os.Exit(1)
	}
}

func scan(root string) ([]finding, error) {
	return scanWithGitCommand(root, newGitCommand)
}

func scanWithGitCommand(root string, command gitCommand) ([]finding, error) {
	files, err := trackedFiles(root, command)
	if err != nil {
		return nil, err
	}
	contents, err := stagedContents(root, files, command)
	if err != nil {
		return nil, err
	}
	packageContexts, err := goCommentPackageContexts(files, contents)
	if err != nil {
		return nil, err
	}
	var findings []finding
	for _, file := range files {
		if file.Mode != "100644" && file.Mode != "100755" {
			continue
		}
		content := contents[file.Path]
		fileProfile, known := profileFor(file.Path)
		if !known || fileProfile == profileNone {
			continue
		}
		commentLines := comments(file.Path, content, fileProfile)
		for commentIndex, comment := range commentLines {
			if isSeparator(comment.Text) && !isSetextUnderline(commentLines, commentIndex) {
				findings = append(findings, finding{Path: file.Path, Line: comment.Line})
			}
		}
		if filepath.Ext(file.Path) == ".go" {
			goFindings, err := goCommentFindingsWithContext(file.Path, content, packageContexts[goCommentPackageTargetKey(file.Path, goCommentPackageKey(file.Path, content))])
			if err != nil {
				return nil, err
			}
			findings = append(findings, goFindings...)
		}
	}
	sort.Slice(findings, func(left, right int) bool {
		if findings[left].Path == findings[right].Path {
			if findings[left].Line == findings[right].Line {
				return findings[left].Message < findings[right].Message
			}
			return findings[left].Line < findings[right].Line
		}
		return findings[left].Path < findings[right].Path
	})
	return findings, nil
}

type goCommentPackageContext struct {
	ShadowedBuiltins map[string]struct{}
}

type goCommentPackageFile struct {
	Path     string
	Shadowed map[string]struct{}
	Build    goCommentBuildInfo
}

type goCommentBuildInfo struct {
	Constraints []constraint.Expr
	GOOS        string
	GOARCH      string
	Cgo         bool
}

func goCommentPackageContexts(files []trackedFile, contents map[string][]byte) (map[string]goCommentPackageContext, error) {
	packageFiles := map[string][]goCommentPackageFile{}
	for _, file := range files {
		base := filepath.Base(file.Path)
		if (file.Mode != "100644" && file.Mode != "100755") || filepath.Ext(file.Path) != ".go" ||
			strings.HasPrefix(base, "_") || strings.HasPrefix(base, ".") {
			continue
		}
		content := contents[file.Path]
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Path, content, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse Go file %s: %w", file.Path, err)
		}
		key := goCommentPackageKey(file.Path, content)
		shadowed := map[string]struct{}{}
		for _, declaration := range parsed.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if declaration.Recv == nil && (declaration.Name.Name == "make" || declaration.Name.Name == "new") {
					shadowed[declaration.Name.Name] = struct{}{}
				}
			case *ast.GenDecl:
				for _, spec := range declaration.Specs {
					var names []*ast.Ident
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						names = []*ast.Ident{spec.Name}
					case *ast.ValueSpec:
						names = spec.Names
					}
					for _, name := range names {
						if name.Name == "make" || name.Name == "new" {
							shadowed[name.Name] = struct{}{}
						}
					}
				}
			}
		}
		buildInfo, err := goCommentBuildInfoWithImports(file.Path, content, parsed)
		if err != nil {
			return nil, err
		}
		packageFiles[key] = append(packageFiles[key], goCommentPackageFile{
			Path:     file.Path,
			Shadowed: shadowed,
			Build:    buildInfo,
		})
	}
	contexts := map[string]goCommentPackageContext{}
	for key, files := range packageFiles {
		for _, target := range files {
			context := goCommentPackageContext{}
			for _, source := range files {
				if !goCommentBuildFilesCompatible(source.Build, target.Build) {
					continue
				}
				if context.ShadowedBuiltins == nil && len(source.Shadowed) > 0 {
					context.ShadowedBuiltins = map[string]struct{}{}
				}
				for name := range source.Shadowed {
					context.ShadowedBuiltins[name] = struct{}{}
				}
			}
			contexts[goCommentPackageTargetKey(target.Path, key)] = context
		}
	}
	return contexts, nil
}

func goCommentPackageKey(path string, content []byte) string {
	file, err := parser.ParseFile(token.NewFileSet(), path, content, 0)
	if err != nil || file.Name == nil {
		return filepath.ToSlash(filepath.Dir(path))
	}
	return filepath.ToSlash(filepath.Dir(path)) + "\x00" + file.Name.Name
}

func goCommentPackageTargetKey(path, packageKey string) string {
	return packageKey + "\x00" + filepath.ToSlash(path)
}

var goCommentKnownGOOS = map[string]struct{}{
	"aix": {}, "android": {}, "darwin": {}, "dragonfly": {}, "freebsd": {}, "hurd": {},
	"illumos": {}, "ios": {}, "js": {}, "linux": {}, "nacl": {}, "netbsd": {}, "openbsd": {},
	"plan9": {}, "solaris": {}, "wasip1": {}, "windows": {}, "zos": {},
}

var goCommentKnownGOARCH = map[string]struct{}{
	"386": {}, "amd64": {}, "amd64p32": {}, "arm": {}, "arm64": {}, "arm64be": {}, "armbe": {},
	"loong64": {}, "mips": {}, "mips64": {}, "mips64le": {}, "mips64p32": {}, "mips64p32le": {},
	"mipsle": {}, "ppc": {}, "ppc64": {}, "ppc64le": {}, "riscv": {}, "riscv64": {}, "s390": {},
	"s390x": {}, "sparc": {}, "sparc64": {}, "wasm": {},
}

type goCommentBuildTarget struct {
	GOOS         string
	GOARCH       string
	CgoSupported bool
}

// goCommentBuildTargets mirrors Go 1.24.13's go tool dist list.
var goCommentBuildTargets = []goCommentBuildTarget{
	{GOOS: "aix", GOARCH: "ppc64", CgoSupported: true},
	{GOOS: "android", GOARCH: "386", CgoSupported: true},
	{GOOS: "android", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "android", GOARCH: "arm", CgoSupported: true},
	{GOOS: "android", GOARCH: "arm64", CgoSupported: true},
	{GOOS: "darwin", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "darwin", GOARCH: "arm64", CgoSupported: true},
	{GOOS: "dragonfly", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "freebsd", GOARCH: "386", CgoSupported: true},
	{GOOS: "freebsd", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "freebsd", GOARCH: "arm", CgoSupported: true},
	{GOOS: "freebsd", GOARCH: "arm64", CgoSupported: true},
	{GOOS: "freebsd", GOARCH: "riscv64", CgoSupported: true},
	{GOOS: "illumos", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "ios", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "ios", GOARCH: "arm64", CgoSupported: true},
	{GOOS: "js", GOARCH: "wasm", CgoSupported: false},
	{GOOS: "linux", GOARCH: "386", CgoSupported: true},
	{GOOS: "linux", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "linux", GOARCH: "arm", CgoSupported: true},
	{GOOS: "linux", GOARCH: "arm64", CgoSupported: true},
	{GOOS: "linux", GOARCH: "loong64", CgoSupported: true},
	{GOOS: "linux", GOARCH: "mips", CgoSupported: true},
	{GOOS: "linux", GOARCH: "mips64", CgoSupported: true},
	{GOOS: "linux", GOARCH: "mips64le", CgoSupported: true},
	{GOOS: "linux", GOARCH: "mipsle", CgoSupported: true},
	{GOOS: "linux", GOARCH: "ppc64", CgoSupported: false},
	{GOOS: "linux", GOARCH: "ppc64le", CgoSupported: true},
	{GOOS: "linux", GOARCH: "riscv64", CgoSupported: true},
	{GOOS: "linux", GOARCH: "s390x", CgoSupported: true},
	{GOOS: "netbsd", GOARCH: "386", CgoSupported: true},
	{GOOS: "netbsd", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "netbsd", GOARCH: "arm", CgoSupported: true},
	{GOOS: "netbsd", GOARCH: "arm64", CgoSupported: true},
	{GOOS: "openbsd", GOARCH: "386", CgoSupported: true},
	{GOOS: "openbsd", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "openbsd", GOARCH: "arm", CgoSupported: true},
	{GOOS: "openbsd", GOARCH: "arm64", CgoSupported: true},
	{GOOS: "openbsd", GOARCH: "ppc64", CgoSupported: false},
	{GOOS: "openbsd", GOARCH: "riscv64", CgoSupported: true},
	{GOOS: "plan9", GOARCH: "386", CgoSupported: false},
	{GOOS: "plan9", GOARCH: "amd64", CgoSupported: false},
	{GOOS: "plan9", GOARCH: "arm", CgoSupported: false},
	{GOOS: "solaris", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "wasip1", GOARCH: "wasm", CgoSupported: false},
	{GOOS: "windows", GOARCH: "386", CgoSupported: true},
	{GOOS: "windows", GOARCH: "amd64", CgoSupported: true},
	{GOOS: "windows", GOARCH: "arm64", CgoSupported: true},
}

func goCommentBuildInfoFor(path string, content []byte) (goCommentBuildInfo, error) {
	info := goCommentBuildInfo{}
	content = bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})
	inBlockComment := false
	var modern constraint.Expr
	var legacy []constraint.Expr
	legacyNeedsBlank := false
	for lineNumber, line := range strings.Split(string(content), "\n") {
		wasInBlockComment := inBlockComment
		trimmed, commentOnly := goCommentBuildHeaderLine(line, &inBlockComment)
		if !commentOnly {
			if legacyNeedsBlank {
				legacyNeedsBlank = false
				legacy = nil
			}
			break
		}
		if trimmed == "" {
			if legacyNeedsBlank {
				if !wasInBlockComment && strings.TrimSpace(line) == "" {
					legacyNeedsBlank = false
				}
			}
			continue
		}
		if goCommentBuildDirectiveLine(trimmed) {
			expression, err := constraint.Parse(trimmed)
			if err != nil {
				return goCommentBuildInfo{}, fmt.Errorf("parse build constraint in %s:%d: %w", path, lineNumber+1, err)
			}
			switch {
			case constraint.IsGoBuild(trimmed):
				if modern != nil {
					return goCommentBuildInfo{}, fmt.Errorf("parse build constraints in %s:%d: duplicate //go:build directive", path, lineNumber+1)
				}
				modern = expression
			case constraint.IsPlusBuild(trimmed):
				legacy = append(legacy, expression)
				legacyNeedsBlank = true
			}
			continue
		}
	}
	if legacyNeedsBlank {
		legacy = nil
	}
	if modern != nil && len(legacy) > 0 {
		canonical, err := constraint.PlusBuildLines(modern)
		if err != nil {
			return goCommentBuildInfo{}, fmt.Errorf("parse paired build constraints in %s: %w", path, err)
		}
		canonicalExprs := make([]constraint.Expr, 0, len(canonical))
		for _, line := range canonical {
			expression, err := constraint.Parse(line)
			if err != nil {
				return goCommentBuildInfo{}, fmt.Errorf("parse paired build constraints in %s: %w", path, err)
			}
			canonicalExprs = append(canonicalExprs, expression)
		}
		if !goCommentBuildExpressionsEquivalent(canonicalExprs, legacy) {
			return goCommentBuildInfo{}, fmt.Errorf("parse build constraints in %s: mismatched //go:build and // +build directives", path)
		}
		info.Constraints = append(info.Constraints, modern)
	} else if modern != nil {
		info.Constraints = append(info.Constraints, modern)
	} else {
		info.Constraints = append(info.Constraints, legacy...)
	}
	info.GOOS, info.GOARCH = goCommentBuildFileTags(path)
	return info, nil
}

func goCommentBuildInfoWithImports(path string, content []byte, file *ast.File) (goCommentBuildInfo, error) {
	info, err := goCommentBuildInfoFor(path, content)
	if err != nil {
		return goCommentBuildInfo{}, err
	}
	info.Cgo = slices.ContainsFunc(file.Imports, func(importSpec *ast.ImportSpec) bool {
		return importSpec.Path != nil && importSpec.Path.Value == `"C"`
	})
	return info, nil
}

func goCommentBuildHeaderLine(line string, inBlockComment *bool) (string, bool) {
	remaining := strings.TrimSpace(line)
	for {
		if *inBlockComment {
			close := strings.Index(remaining, "*/")
			if close < 0 {
				return "", true
			}
			*inBlockComment = false
			remaining = strings.TrimSpace(remaining[close+len("*/"):])
			continue
		}
		switch {
		case remaining == "":
			return "", true
		case strings.HasPrefix(remaining, "/*"):
			close := strings.Index(remaining[2:], "*/")
			if close < 0 {
				*inBlockComment = true
				return "", true
			}
			remaining = strings.TrimSpace(remaining[2+close+len("*/"):])
		case strings.HasPrefix(remaining, "//"):
			return remaining, true
		default:
			return remaining, false
		}
	}
}

func goCommentBuildExpressionsEquivalent(left, right []constraint.Expr) bool {
	leftCanonical, leftOK := goCommentBuildCanonicalPlusLines(left)
	rightCanonical, rightOK := goCommentBuildCanonicalPlusLines(right)
	if leftOK && rightOK && slices.Equal(leftCanonical, rightCanonical) {
		return true
	}
	tags := map[string]struct{}{}
	for _, expressions := range [][]constraint.Expr{left, right} {
		for _, expression := range expressions {
			goCommentBuildConstraintTags(expression, tags)
		}
	}
	if len(tags) > 12 {
		return false
	}
	ordered := make([]string, 0, len(tags))
	for tag := range tags {
		ordered = append(ordered, tag)
	}
	slices.Sort(ordered)
	for mask := uint64(0); mask < uint64(1)<<len(ordered); mask++ {
		values := make(map[string]bool, len(ordered))
		for index, tag := range ordered {
			values[tag] = mask&(uint64(1)<<index) != 0
		}
		leftValue := true
		for _, expression := range left {
			leftValue = leftValue && expression.Eval(func(tag string) bool { return values[tag] })
		}
		rightValue := true
		for _, expression := range right {
			rightValue = rightValue && expression.Eval(func(tag string) bool { return values[tag] })
		}
		if leftValue != rightValue {
			return false
		}
	}
	return true
}

func goCommentBuildCanonicalPlusLines(expressions []constraint.Expr) ([]string, bool) {
	lines := make([]string, 0, len(expressions))
	for _, expression := range expressions {
		canonical, err := constraint.PlusBuildLines(expression)
		if err != nil {
			return nil, false
		}
		lines = append(lines, canonical...)
	}
	slices.Sort(lines)
	return lines, true
}

func goCommentBuildDirectiveLine(line string) bool {
	return strings.HasPrefix(line, "//go:build") && goCommentDirectiveBoundary(line, len("//go:build")) ||
		(strings.HasPrefix(line, "// +build") && goCommentDirectiveBoundary(line, len("// +build"))) ||
		(strings.HasPrefix(line, "//+build") && goCommentDirectiveBoundary(line, len("//+build")))
}

func goCommentDirectiveBoundary(text string, offset int) bool {
	return offset == len(text) || text[offset] == ' ' || text[offset] == '\t'
}

func goCommentBuildFileTags(path string) (string, string) {
	name, _, _ := strings.Cut(filepath.Base(path), ".")
	underscore := strings.IndexByte(name, '_')
	if underscore < 0 {
		return "", ""
	}
	parts := strings.Split(name[underscore:], "_")
	if len(parts) > 0 && parts[len(parts)-1] == "test" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) >= 3 {
		goos, goarch := parts[len(parts)-2], parts[len(parts)-1]
		if _, known := goCommentKnownGOOS[goos]; known {
			if _, known := goCommentKnownGOARCH[goarch]; known {
				return goos, goarch
			}
		}
	}
	if len(parts) >= 2 {
		last := parts[len(parts)-1]
		if _, known := goCommentKnownGOOS[last]; known {
			return last, ""
		}
		if _, known := goCommentKnownGOARCH[last]; known {
			return "", last
		}
	}
	return "", ""
}

func goCommentBuildFilesCompatible(source, target goCommentBuildInfo) bool {
	if source.GOARCH != "" && target.GOARCH != "" && source.GOARCH != target.GOARCH {
		return false
	}
	tags := map[string]struct{}{}
	sourceConstraints, targetConstraints := source.Constraints, target.Constraints
	for _, expression := range append(sourceConstraints, targetConstraints...) {
		goCommentBuildConstraintTags(expression, tags)
	}
	variableTags := make([]string, 0, len(tags))
	for tag := range tags {
		if goCommentBuildTagIsFixed(tag) {
			continue
		}
		variableTags = append(variableTags, tag)
	}
	slices.Sort(variableTags)
	if len(variableTags) >= 8 {
		return goCommentBuildHasKnownCompatibleTarget(source, target, variableTags)
	}
	for _, candidate := range goCommentBuildTargets {
		if !goCommentBuildTargetMatches(source, candidate) || !goCommentBuildTargetMatches(target, candidate) {
			continue
		}
		for _, compiler := range []string{"gc", "gccgo"} {
			cgoModes := []bool{false}
			if candidate.CgoSupported {
				cgoModes = append(cgoModes, true)
			}
			for _, cgoEnabled := range cgoModes {
				if (source.Cgo || target.Cgo) && !cgoEnabled {
					continue
				}
				for _, featureTags := range goCommentBuildFeatureVariants(candidate) {
					known := goCommentBuildKnownTags(candidate.GOOS, candidate.GOARCH, compiler, cgoEnabled)
					maps.Copy(known, featureTags)
					for mask := uint64(0); mask < uint64(1)<<len(variableTags); mask++ {
						values := map[string]bool{}
						for idx, tag := range variableTags {
							values[tag] = mask&(uint64(1)<<idx) != 0
						}
						if goCommentBuildConstraintsMatch(sourceConstraints, known, values) &&
							goCommentBuildConstraintsMatch(targetConstraints, known, values) {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func goCommentBuildHasKnownCompatibleTarget(source, target goCommentBuildInfo, variableTags []string) bool {
	if source.GOARCH != "" && target.GOARCH != "" && source.GOARCH != target.GOARCH {
		return false
	}
	for _, candidate := range goCommentBuildTargets {
		if !goCommentBuildTargetMatches(source, candidate) || !goCommentBuildTargetMatches(target, candidate) {
			continue
		}
		for _, compiler := range []string{"gc", "gccgo"} {
			cgoModes := []bool{false}
			if candidate.CgoSupported {
				cgoModes = append(cgoModes, true)
			}
			for _, cgoEnabled := range cgoModes {
				if (source.Cgo || target.Cgo) && !cgoEnabled {
					continue
				}
				for _, featureTags := range goCommentBuildFeatureVariants(candidate) {
					known := goCommentBuildKnownTags(candidate.GOOS, candidate.GOARCH, compiler, cgoEnabled)
					maps.Copy(known, featureTags)
					if goCommentBuildConstraintsDefinitelyFalse(source.Constraints, known) ||
						goCommentBuildConstraintsDefinitelyFalse(target.Constraints, known) {
						continue
					}
					satisfiable, proven := goCommentBuildConstraintsSatisfiable(source.Constraints, target.Constraints, known, variableTags)
					if satisfiable || !proven {
						return true
					}
				}
			}
		}
	}
	return false
}

func goCommentBuildTargetMatches(info goCommentBuildInfo, target goCommentBuildTarget) bool {
	return (info.GOARCH == "" || info.GOARCH == target.GOARCH) &&
		goCommentBuildFileGOOSMatches(info.GOOS, target.GOOS)
}

func goCommentBuildKnownTags(goos, goarch, compiler string, cgoEnabled bool) map[string]bool {
	known := map[string]bool{}
	for tag := range goCommentKnownGOOS {
		known[tag] = goCommentBuildTagMatches(tag, goos, goarch)
	}
	for tag := range goCommentKnownGOARCH {
		known[tag] = tag == goarch
	}
	known["unix"] = goCommentBuildTagMatches("unix", goos, goarch)
	for _, tag := range gobuild.Default.ReleaseTags {
		known[tag] = true
	}
	known["gc"] = compiler == "gc"
	known["gccgo"] = compiler == "gccgo"
	known["cgo"] = cgoEnabled
	for _, tags := range goCommentBuildFeatureTagsByArch {
		for _, tag := range tags {
			known[tag] = false
		}
	}
	return known
}

var goCommentBuildFeatureTagsByArch = map[string][]string{
	"386":      {"386.387", "386.sse2"},
	"amd64":    {"amd64.v1", "amd64.v2", "amd64.v3", "amd64.v4"},
	"arm":      {"arm.5", "arm.6", "arm.7"},
	"arm64":    {"arm64.v8.0", "arm64.v8.1", "arm64.v8.2", "arm64.v8.3", "arm64.v8.4", "arm64.v8.5", "arm64.v8.6", "arm64.v8.7", "arm64.v8.8", "arm64.v8.9", "arm64.v9.0", "arm64.v9.1", "arm64.v9.2", "arm64.v9.3", "arm64.v9.4", "arm64.v9.5"},
	"mips":     {"mips.hardfloat", "mips.softfloat"},
	"mipsle":   {"mipsle.hardfloat", "mipsle.softfloat"},
	"mips64":   {"mips64.hardfloat", "mips64.softfloat"},
	"mips64le": {"mips64le.hardfloat", "mips64le.softfloat"},
	"ppc64":    {"ppc64.power8", "ppc64.power9", "ppc64.power10"},
	"ppc64le":  {"ppc64le.power8", "ppc64le.power9", "ppc64le.power10"},
	"riscv64":  {"riscv64.rva20u64", "riscv64.rva22u64"},
	"wasm":     {"wasm.satconv", "wasm.signext"},
}

func goCommentBuildFeatureTagIsFixed(tag string) bool {
	for _, tags := range goCommentBuildFeatureTagsByArch {
		if slices.Contains(tags, tag) {
			return true
		}
	}
	return false
}

func goCommentBuildFeatureVariants(target goCommentBuildTarget) []map[string]bool {
	levels := func(prefix string, names []string) []map[string]bool {
		variants := make([]map[string]bool, 0, len(names))
		for level := range names {
			values := make(map[string]bool, len(names))
			for index, name := range names {
				values[prefix+name] = index <= level
			}
			variants = append(variants, values)
		}
		return variants
	}
	choices := func(prefix string, names []string) []map[string]bool {
		variants := make([]map[string]bool, 0, len(names))
		for chosen := range names {
			values := make(map[string]bool, len(names))
			for index, name := range names {
				values[prefix+name] = index == chosen
			}
			variants = append(variants, values)
		}
		return variants
	}
	features := func(prefix string, names []string) []map[string]bool {
		variants := make([]map[string]bool, 0, 1<<len(names))
		for mask := 0; mask < 1<<len(names); mask++ {
			values := make(map[string]bool, len(names))
			for index, name := range names {
				values[prefix+name] = mask&(1<<index) != 0
			}
			variants = append(variants, values)
		}
		return variants
	}
	switch target.GOARCH {
	case "386":
		return choices("386.", []string{"387", "sse2"})
	case "amd64":
		return levels("amd64.", []string{"v1", "v2", "v3", "v4"})
	case "arm":
		return levels("arm.", []string{"5", "6", "7"})
	case "arm64":
		versions := []string{
			"v8.0", "v8.1", "v8.2", "v8.3", "v8.4", "v8.5", "v8.6", "v8.7", "v8.8", "v8.9",
			"v9.0", "v9.1", "v9.2", "v9.3", "v9.4", "v9.5",
		}
		variants := make([]map[string]bool, 0, len(versions))
		for _, version := range versions {
			major := version[1] - '0'
			minor := version[3] - '0'
			values := make(map[string]bool, len(versions))
			for _, feature := range versions {
				values["arm64."+feature] = false
			}
			for level := byte(0); level <= minor; level++ {
				values[fmt.Sprintf("arm64.v%d.%d", major, level)] = true
			}
			if major == 9 {
				for level := byte(0); level <= minor+5 && level <= 9; level++ {
					values[fmt.Sprintf("arm64.v8.%d", level)] = true
				}
			}
			variants = append(variants, values)
		}
		return variants
	case "mips", "mipsle", "mips64", "mips64le":
		return choices(target.GOARCH+".", []string{"hardfloat", "softfloat"})
	case "ppc64", "ppc64le":
		return levels(target.GOARCH+".", []string{"power8", "power9", "power10"})
	case "riscv64":
		return levels("riscv64.", []string{"rva20u64", "rva22u64"})
	case "wasm":
		return features("wasm.", []string{"satconv", "signext"})
	default:
		return []map[string]bool{{}}
	}
}

func goCommentBuildTagIsFixed(tag string) bool {
	if _, known := goCommentKnownGOOS[tag]; known {
		return true
	}
	if _, known := goCommentKnownGOARCH[tag]; known {
		return true
	}
	if tag == "unix" || tag == "gc" || tag == "gccgo" || tag == "cgo" {
		return true
	}
	if goCommentBuildFeatureTagIsFixed(tag) {
		return true
	}
	if slices.Contains(gobuild.Default.ReleaseTags, tag) {
		return true
	}
	_, releaseTag := goCommentBuildReleaseTagValue(tag)
	return releaseTag
}

func goCommentBuildReleaseTagValue(tag string) (int, bool) {
	if !strings.HasPrefix(tag, "go1.") {
		return 0, false
	}
	suffix := tag[len("go1."):]
	if suffix == "" || suffix[0] < '1' || suffix[0] > '9' {
		return 0, false
	}
	for _, value := range suffix[1:] {
		if value < '0' || value > '9' {
			return 0, false
		}
	}
	if slices.Contains(gobuild.Default.ReleaseTags, tag) {
		return 1, true
	}
	return -1, true
}

func goCommentBuildConstraintsDefinitelyFalse(constraints []constraint.Expr, known map[string]bool) bool {
	for _, expression := range constraints {
		if goCommentBuildConstraintValue(expression, known) == -1 {
			return true
		}
	}
	return false
}

func goCommentBuildConstraintsSatisfiable(source, target []constraint.Expr, known map[string]bool, variableTags []string) (bool, bool) {
	budget := 4096
	var search func(int) bool
	search = func(index int) bool {
		if budget == 0 {
			return false
		}
		budget--
		for _, constraints := range [][]constraint.Expr{source, target} {
			for _, expression := range constraints {
				if goCommentBuildConstraintValue(expression, known) == -1 {
					return false
				}
			}
		}
		if index == len(variableTags) {
			return true
		}
		tag := variableTags[index]
		if _, alreadyKnown := known[tag]; alreadyKnown {
			return search(index + 1)
		}
		known[tag] = true
		if search(index + 1) {
			delete(known, tag)
			return true
		}
		known[tag] = false
		if search(index + 1) {
			delete(known, tag)
			return true
		}
		delete(known, tag)
		return false
	}
	if search(0) {
		return true, true
	}
	return false, budget > 0
}

func goCommentBuildConstraintValue(expression constraint.Expr, known map[string]bool) int {
	switch expression := expression.(type) {
	case *constraint.TagExpr:
		value, found := known[expression.Tag]
		if !found {
			if releaseValue, releaseTag := goCommentBuildReleaseTagValue(expression.Tag); releaseTag {
				return releaseValue
			}
			return 0
		}
		if value {
			return 1
		}
		return -1
	case *constraint.NotExpr:
		value := goCommentBuildConstraintValue(expression.X, known)
		return -value
	case *constraint.AndExpr:
		left := goCommentBuildConstraintValue(expression.X, known)
		right := goCommentBuildConstraintValue(expression.Y, known)
		if left == -1 || right == -1 {
			return -1
		}
		if left == 1 && right == 1 {
			return 1
		}
		return 0
	case *constraint.OrExpr:
		left := goCommentBuildConstraintValue(expression.X, known)
		right := goCommentBuildConstraintValue(expression.Y, known)
		if left == 1 || right == 1 {
			return 1
		}
		if left == -1 && right == -1 {
			return -1
		}
		return 0
	default:
		return 0
	}
}

func goCommentBuildConstraintTags(expression constraint.Expr, tags map[string]struct{}) {
	switch expression := expression.(type) {
	case *constraint.TagExpr:
		tags[expression.Tag] = struct{}{}
	case *constraint.NotExpr:
		goCommentBuildConstraintTags(expression.X, tags)
	case *constraint.AndExpr:
		goCommentBuildConstraintTags(expression.X, tags)
		goCommentBuildConstraintTags(expression.Y, tags)
	case *constraint.OrExpr:
		goCommentBuildConstraintTags(expression.X, tags)
		goCommentBuildConstraintTags(expression.Y, tags)
	}
}

func goCommentBuildFileGOOSMatches(wanted, actual string) bool {
	if wanted == "" || wanted == actual {
		return true
	}
	switch {
	case actual == "android" && wanted == "linux":
		return true
	case actual == "illumos" && wanted == "solaris":
		return true
	case actual == "ios" && wanted == "darwin":
		return true
	default:
		return false
	}
}

func goCommentBuildTagMatches(tag, goos, goarch string) bool {
	if tag == goos || tag == goarch {
		return true
	}
	switch {
	case goos == "android" && tag == "linux":
		return true
	case goos == "illumos" && tag == "solaris":
		return true
	case goos == "ios" && tag == "darwin":
		return true
	case tag == "unix":
		_, found := goCommentUnixGOOS[goos]
		return found
	default:
		return false
	}
}

var goCommentUnixGOOS = map[string]struct{}{
	"aix": {}, "android": {}, "darwin": {}, "dragonfly": {}, "freebsd": {}, "hurd": {},
	"illumos": {}, "ios": {}, "linux": {}, "netbsd": {}, "openbsd": {}, "solaris": {},
}

func goCommentBuildConstraintsMatch(constraints []constraint.Expr, known, values map[string]bool) bool {
	for _, expression := range constraints {
		if !expression.Eval(func(tag string) bool {
			if value, found := known[tag]; found {
				return value
			}
			return values[tag]
		}) {
			return false
		}
	}
	return true
}

func trackedFiles(root string, command gitCommand) ([]trackedFile, error) {
	output, err := command(root, "ls-files", "--cached", "--stage", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	return parseTrackedFiles(output), nil
}

func parseTrackedFiles(output []byte) []trackedFile {
	records := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	if len(records) == 1 && records[0] == "" {
		return nil
	}
	files := make([]trackedFile, 0, len(records))
	for _, record := range records {
		metadata, path, found := strings.Cut(record, "\t")
		fields := strings.Fields(metadata)
		if !found || len(fields) != 3 || fields[2] != "0" {
			continue
		}
		files = append(files, trackedFile{Path: path, Mode: fields[0], Hash: fields[1]})
	}
	return files
}

func stagedContents(root string, files []trackedFile, command gitCommand) (map[string][]byte, error) {
	var request strings.Builder
	for _, file := range files {
		if file.Mode == "100644" || file.Mode == "100755" {
			fmt.Fprintln(&request, file.Hash)
		}
	}
	gitCommand := command(root, "cat-file", "--batch")
	gitCommand.Stdin = strings.NewReader(request.String())
	output, err := gitCommand.Output()
	if err != nil {
		return nil, fmt.Errorf("read staged files: %w", err)
	}
	reader := bufio.NewReader(bytes.NewReader(output))
	contents := map[string][]byte{}
	for _, file := range files {
		if file.Mode != "100644" && file.Mode != "100755" {
			continue
		}
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read staged %s header: %w", file.Path, err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != file.Hash || fields[1] != "blob" {
			return nil, fmt.Errorf("read staged %s: unexpected object header %q", file.Path, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("read staged %s size: %w", file.Path, err)
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			return nil, fmt.Errorf("read staged %s content: %w", file.Path, err)
		}
		separator, err := reader.ReadByte()
		if err != nil || separator != '\n' {
			return nil, fmt.Errorf("read staged %s separator", file.Path)
		}
		contents[file.Path] = content
	}
	return contents, nil
}

func newGitCommand(root string, arguments ...string) *exec.Cmd {
	return exec.Command("git", append([]string{"-C", root}, arguments...)...)
}

func profileFor(path string) (profile, bool) {
	base := filepath.Base(path)
	if base == "Makefile" || base == "Justfile" || strings.Contains(base, "Dockerfile") || base == ".gitignore" || base == ".dockerignore" || base == "commit-msg" || base == "pre-commit" || base == "rules" || base == "CODEOWNERS" {
		return profileHash, true
	}
	if base == ".clang-format" || base == ".clang-tidy" || base == ".gitmodules" {
		return profileHash, true
	}
	if base == "AUTHORS" || base == "LICENSE" || base == "changelog" || base == "compat" || base == "control" || base == "format" || base == "options" || base == "meta-data" || strings.Contains(path, "/testdata/fuzz/") {
		return profileNone, true
	}
	switch filepath.Ext(path) {
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".go", ".rs", ".ts", ".tsx", ".js", ".jsx", ".proto", ".css", ".scss", ".java", ".kt", ".mod":
		return profileSlash, true
	case ".py", ".sh", ".bash", ".zsh", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf", ".service", ".install", ".env", ".org", ".npmrc", ".build", ".dev":
		if filepath.Ext(path) == ".ini" || filepath.Ext(path) == ".service" || filepath.Ext(path) == ".conf" || filepath.Ext(path) == ".cfg" {
			return profileINI, true
		}
		return profileHash, true
	case ".lock":
		if base == "Cargo.lock" {
			return profileHash, true
		}
		return profileNone, false
	case ".html", ".htm", ".xml", ".svg", ".md", ".markdown":
		return profileMarkup, true
	case ".sql":
		return profileSQL, true
	case ".json", ".sum", ".txt", ".worktreeinclude":
		return profileNone, true
	case "":
	}
	return profileNone, false
}

type comment struct {
	Line int
	Text string
}

func comments(path string, content []byte, fileProfile profile) []comment {
	switch fileProfile {
	case profileSlash:
		return slashCommentsForPath(path, content)
	case profileHash:
		return hashCommentsForPath(path, content)
	case profileINI:
		return iniComments(content)
	case profileMarkup:
		return markupCommentsForPath(path, content)
	case profileSQL:
		return sqlComments(content)
	default:
		return nil
	}
}

func slashComments(content []byte) []comment {
	return slashCommentsForPath("", content)
}

func slashCommentsForPath(path string, content []byte) []comment {
	var comments []comment
	line := 1
	isRust := filepath.Ext(path) == ".rs"
	isJava := filepath.Ext(path) == ".java"
	isTripleQuotedLanguage := isJava || filepath.Ext(path) == ".kt"
	isTemplateLanguage := filepath.Ext(path) == ".ts" || filepath.Ext(path) == ".tsx" || filepath.Ext(path) == ".js" || filepath.Ext(path) == ".jsx"
	for offset := 0; offset < len(content); {
		if content[offset] == '\n' {
			line++
			offset++
			continue
		}
		if content[offset] == '`' && isTemplateLanguage {
			extracted, next := templateLiteralComments(path, content, offset, line)
			comments = append(comments, extracted...)
			line += bytes.Count(content[offset:next], []byte("\n"))
			offset = next
			continue
		}
		if isTripleQuotedLanguage && bytes.HasPrefix(content[offset:], []byte(`"""`)) {
			next := skipTripleQuoted(content, offset, isJava)
			line += bytes.Count(content[offset:next], []byte("\n"))
			offset = next
			continue
		}
		if content[offset] == '"' || content[offset] == '`' || (content[offset] == '\'' && (!isRust || !isRustLifetime(content, offset))) {
			next := skipQuoted(content, offset, content[offset])
			line += bytes.Count(content[offset:next], []byte("\n"))
			offset = next
			continue
		}
		if content[offset] == 'r' || (content[offset] == 'R' && offset+1 < len(content) && content[offset+1] == '"') || (content[offset] == 'b' && offset+1 < len(content) && (content[offset+1] == 'r' || content[offset+1] == 'R')) {
			next := skipRustRaw(content, offset)
			line += bytes.Count(content[offset:next], []byte("\n"))
			offset = next
			continue
		}
		if offset+1 < len(content) && content[offset] == '/' && content[offset+1] == '/' {
			end := bytes.IndexByte(content[offset:], '\n')
			if end < 0 {
				end = len(content) - offset
			}
			text := string(content[offset+2 : offset+end])
			if len(text) > 0 && (text[0] == '!' || text[0] == '/' && (len(text) == 1 || text[1] != '/')) {
				text = text[1:]
			}
			comments = append(comments, comment{Line: line, Text: text})
			offset += end
			continue
		}
		if offset+1 < len(content) && content[offset] == '/' && content[offset+1] == '*' {
			end := blockCommentEnd(content, offset+2, isRust)
			body := content[offset+2 : offset+2+end]
			lines := strings.Split(string(body), "\n")
			for idx, text := range lines {
				text = strings.TrimLeft(text, " \t")
				if len(text) > 1 && text[0] == '*' && (text[1] == ' ' || text[1] == '\t') {
					text = text[1:]
				}
				lines[idx] = text
			}
			comments = append(comments, comment{Line: line, Text: strings.Join(lines, "\n")})
			line += bytes.Count(body, []byte("\n"))
			offset += end + 4
			continue
		}
		offset++
	}
	return comments
}

func templateLiteralComments(path string, content []byte, offset, line int) ([]comment, int) {
	var comments []comment
	templateStart := offset
	for offset++; offset < len(content); offset++ {
		if content[offset] == '\\' {
			offset++
			continue
		}
		if content[offset] == '`' {
			return comments, offset + 1
		}
		if content[offset] != '$' || offset+1 == len(content) || content[offset+1] != '{' {
			continue
		}
		start := offset + 2
		end := templateInterpolationEnd(content, start)
		if end == len(content) {
			return comments, len(content)
		}
		inner := slashCommentsForPath(path, content[start:end])
		baseLine := line + bytes.Count(content[templateStart:start], []byte("\n"))
		for _, current := range inner {
			current.Line += baseLine - 1
			comments = append(comments, current)
		}
		offset = end
	}
	return comments, offset
}

func templateInterpolationEnd(content []byte, offset int) int {
	depth := 1
	canStartRegex := true
	for offset < len(content) {
		switch content[offset] {
		case '\'', '"':
			offset = skipQuoted(content, offset, content[offset])
			canStartRegex = false
			continue
		case '`':
			offset = templateLiteralEnd(content, offset)
			canStartRegex = false
			continue
		case '/':
			if offset+1 < len(content) && content[offset+1] == '/' {
				end := bytes.IndexByte(content[offset:], '\n')
				if end < 0 {
					return len(content)
				}
				offset += end + 1
				canStartRegex = false
				continue
			}
			if offset+1 < len(content) && content[offset+1] == '*' {
				end := bytes.Index(content[offset+2:], []byte("*/"))
				if end < 0 {
					return len(content)
				}
				offset += end + 4
				canStartRegex = false
				continue
			}
			if canStartRegex {
				offset = skipJavaScriptRegex(content, offset)
				canStartRegex = false
				continue
			}
			canStartRegex = true
		case '{':
			depth++
			canStartRegex = true
		case '}':
			depth--
			if depth == 0 {
				return offset
			}
			canStartRegex = false
		case '(', '[', ',', ':', ';', '?', '=', '!', '+', '-', '*', '%', '&', '|', '^', '~', '<', '>':
			canStartRegex = true
		case ')', ']':
			canStartRegex = false
		default:
			if isJavaScriptIdentifierStart(content[offset]) {
				start := offset
				offset++
				for offset < len(content) && isJavaScriptIdentifierPart(content[offset]) {
					offset++
				}
				canStartRegex = isJavaScriptRegexPrefixKeyword(string(content[start:offset])) && (start == 0 || content[start-1] != '.')
				continue
			}
			if content[offset] != ' ' && content[offset] != '\t' && content[offset] != '\n' && content[offset] != '\r' {
				canStartRegex = false
			}
		}
		offset++
	}
	return len(content)
}

func isJavaScriptIdentifierStart(value byte) bool {
	return value == '_' || value == '$' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isJavaScriptIdentifierPart(value byte) bool {
	return isJavaScriptIdentifierStart(value) || value >= '0' && value <= '9'
}

func isJavaScriptRegexPrefixKeyword(identifier string) bool {
	switch identifier {
	case "await", "case", "delete", "do", "else", "in", "instanceof", "new", "of", "return", "throw", "typeof", "void", "yield":
		return true
	default:
		return false
	}
}

func skipJavaScriptRegex(content []byte, offset int) int {
	inCharacterClass := false
	for offset++; offset < len(content); offset++ {
		if content[offset] == '\\' {
			offset++
			continue
		}
		if content[offset] == '[' {
			inCharacterClass = true
			continue
		}
		if content[offset] == ']' {
			inCharacterClass = false
			continue
		}
		if content[offset] == '/' && !inCharacterClass {
			return offset + 1
		}
		if content[offset] == '\n' {
			return len(content)
		}
	}
	return len(content)
}

func templateLiteralEnd(content []byte, offset int) int {
	for offset++; offset < len(content); offset++ {
		if content[offset] == '\\' {
			offset++
			continue
		}
		if content[offset] == '`' {
			return offset + 1
		}
		if content[offset] == '$' && offset+1 < len(content) && content[offset+1] == '{' {
			offset = templateInterpolationEnd(content, offset+2)
		}
	}
	return len(content)
}

func blockCommentEnd(content []byte, offset int, nested bool) int {
	start := offset
	depth := 1
	for offset+1 < len(content) {
		if nested && content[offset] == '/' && content[offset+1] == '*' {
			depth++
			offset += 2
			continue
		}
		if content[offset] == '*' && content[offset+1] == '/' {
			depth--
			if depth == 0 {
				return offset - start
			}
			offset += 2
			continue
		}
		offset++
	}
	return len(content) - start
}

func isRustLifetime(content []byte, offset int) bool {
	idx := offset + 1
	if idx == len(content) || !(content[idx] == '_' || content[idx] >= 'a' && content[idx] <= 'z' || content[idx] >= 'A' && content[idx] <= 'Z') {
		return false
	}
	for idx < len(content) && (content[idx] == '_' || content[idx] >= 'a' && content[idx] <= 'z' || content[idx] >= 'A' && content[idx] <= 'Z' || content[idx] >= '0' && content[idx] <= '9') {
		idx++
	}
	return idx == len(content) || content[idx] != '\''
}

func skipQuoted(content []byte, offset int, quote byte) int {
	offset++
	for offset < len(content) {
		if content[offset] == '\\' && quote != '`' {
			offset += 2
			continue
		}
		if offset < len(content) && content[offset] == quote {
			return offset + 1
		}
		offset++
	}
	return offset
}

func skipTripleQuoted(content []byte, offset int, escapedDelimiters bool) int {
	for offset += 3; offset+2 < len(content); offset++ {
		if !bytes.HasPrefix(content[offset:], []byte(`"""`)) {
			continue
		}
		if !escapedDelimiters || !escapedAt(content, offset) {
			return offset + 3
		}
		offset += 2
	}
	return len(content)
}

func escapedAt(content []byte, offset int) bool {
	escaped := false
	for offset--; offset >= 0 && content[offset] == '\\'; offset-- {
		escaped = !escaped
	}
	return escaped
}

func skipRustRaw(content []byte, offset int) int {
	start := offset
	if content[offset] == 'R' {
		endTag := bytes.IndexByte(content[offset+1:], '(')
		if endTag < 0 {
			return start + 1
		}
		tag := content[offset+2 : offset+1+endTag]
		end := append([]byte(")"), tag...)
		end = append(end, '"')
		match := bytes.Index(content[offset+2+endTag:], end)
		if match < 0 {
			return len(content)
		}
		return offset + 2 + endTag + match + len(end)
	}
	if content[offset] == 'b' {
		offset++
	}
	offset++
	hashes := 0
	for offset < len(content) && content[offset] == '#' {
		hashes++
		offset++
	}
	if offset == len(content) || content[offset] != '"' {
		return start + 1
	}
	end := []byte("\"" + strings.Repeat("#", hashes))
	match := bytes.Index(content[offset+1:], end)
	if match < 0 {
		return len(content)
	}
	return offset + 1 + match + len(end)
}

func hashComments(content []byte) []comment {
	return hashCommentsForPath("", content)
}

func hashCommentsForPath(path string, content []byte) []comment {
	var comments []comment
	lines := strings.Split(string(content), "\n")
	var heredocs []shellHeredoc
	triple := ""
	base := filepath.Base(path)
	hashCommentsStartLines := strings.Contains(base, "Dockerfile") ||
		base == ".gitignore" || base == ".dockerignore"
	isPython := filepath.Ext(path) == ".py"
	isTOML := filepath.Ext(path) == ".toml"
	isShell := isShellPath(path)
	var shellState shellLexState
	yamlParentIndent := -1
	yamlContentIndent := -1
	for idx, line := range lines {
		if yamlParentIndent >= 0 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			indent := leadingSpaces(line)
			if yamlContentIndent < 0 && indent > yamlParentIndent {
				yamlContentIndent = indent
				continue
			}
			if yamlContentIndent >= 0 && indent >= yamlContentIndent {
				continue
			}
			yamlParentIndent = -1
			yamlContentIndent = -1
		}
		if len(heredocs) != 0 {
			candidate := strings.TrimSuffix(line, "\r")
			if heredocs[0].StripTabs {
				candidate = strings.TrimLeft(candidate, "\t")
			}
			if candidate == heredocs[0].Marker {
				heredocs = heredocs[1:]
			}
			continue
		}
		if triple != "" {
			if close := unescapedIndex(line, triple); close >= 0 {
				delimiterLength := len(triple)
				triple = ""
				if position := hashCommentStart(line[close+delimiterLength:]); position >= 0 {
					start := close + delimiterLength + position
					comments = append(comments, comment{Line: idx + 1, Text: line[start+1:]})
				}
			}
			continue
		}
		lineStartShellState := shellState
		commentStart := hashCommentStart(line)
		if hashCommentsStartLines {
			commentStart = lineHashCommentStart(line)
		}
		if isShell {
			commentStart = shellCommentStart(line, &shellState)
		}
		code := line
		if commentStart >= 0 {
			code = line[:commentStart]
		}
		if markers := shellHeredocMarkers(code, isShell, lineStartShellState); len(markers) != 0 {
			if commentStart >= 0 {
				comments = append(comments, comment{Line: idx + 1, Text: line[commentStart+1:]})
			}
			heredocs = append(heredocs, markers...)
			continue
		}
		delimiter := tripleDelimiter(code)
		if (isPython || isTOML) && delimiter != "" && strings.Count(code, delimiter) >= 2 {
			if commentStart >= 0 {
				comments = append(comments, comment{Line: idx + 1, Text: line[commentStart+1:]})
			}
			continue
		}
		if (isPython || isTOML) && delimiter != "" {
			triple = delimiter
			continue
		}
		if isYAML(path) && yamlScalar(line) {
			yamlParentIndent = leadingSpaces(line)
			yamlContentIndent = -1
		}
		if isYAML(path) {
			commentStart = yamlCommentStart(line)
		}
		if commentStart >= 0 {
			comments = append(comments, comment{Line: idx + 1, Text: line[commentStart+1:]})
		}
	}
	return comments
}

func lineHashCommentStart(line string) int {
	for idx := 0; idx < len(line); idx++ {
		if line[idx] == ' ' || line[idx] == '\t' {
			continue
		}
		if line[idx] == '#' {
			return idx
		}
		break
	}
	return -1
}

func unescapedIndex(text, needle string) int {
	for idx := 0; idx+len(needle) <= len(text); idx++ {
		if text[idx:idx+len(needle)] != needle {
			continue
		}
		escaped := false
		for previous := idx - 1; previous >= 0 && text[previous] == '\\'; previous-- {
			escaped = !escaped
		}
		if !escaped {
			return idx
		}
	}
	return -1
}

func tripleDelimiter(code string) string {
	inSingle, inDouble := false, false
	for idx := 0; idx < len(code); idx++ {
		if code[idx] == '\\' {
			idx++
			continue
		}
		if !inDouble && strings.HasPrefix(code[idx:], "'''") && !inSingle {
			return "'''"
		}
		if !inSingle && strings.HasPrefix(code[idx:], `"""`) && !inDouble {
			return `"""`
		}
		if code[idx] == '\'' && !inDouble {
			inSingle = !inSingle
		}
		if code[idx] == '"' && !inSingle {
			inDouble = !inDouble
		}
	}
	return ""
}

func isYAML(path string) bool { return filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml" }

func leadingSpaces(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

func yamlScalar(line string) bool {
	value := strings.TrimSpace(line)
	if remainder, found := strings.CutPrefix(value, "-"); found {
		value = strings.TrimSpace(remainder)
	}
	if _, remainder, found := strings.Cut(value, ":"); found {
		value = remainder
	}
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">")
}

func yamlCommentStart(line string) int {
	for idx := range line {
		if line[idx] == '#' && (idx == 0 || line[idx-1] == ' ' || line[idx-1] == '\t') {
			return idx
		}
	}
	return -1
}

func isShellPath(path string) bool {
	base := filepath.Base(path)
	return filepath.Ext(path) == ".sh" || filepath.Ext(path) == ".bash" || filepath.Ext(path) == ".zsh" || base == "pre-commit" || base == "commit-msg"
}

func shellHeredocMarkers(line string, isShell bool, initialState shellLexState) []shellHeredoc {
	if !isShell {
		return nil
	}
	var heredocs []shellHeredoc
	inSingle := initialState.Quote == '\''
	inDouble := initialState.Quote == '"'
	commandDepth := initialState.CommandDepth
	resumeDoubleDepths := append([]int(nil), initialState.ResumeDoubleDepths...)
	arithmeticDepth := 0
	for offset := 0; offset+1 < len(line); {
		if line[offset] == '\\' {
			offset += 2
			continue
		}
		if line[offset] == '\'' && !inDouble {
			inSingle = !inSingle
			offset++
			continue
		}
		if line[offset] == '"' && !inSingle {
			inDouble = !inDouble
			offset++
			continue
		}
		if inDouble && line[offset] == '$' && line[offset+1] == '(' {
			inDouble = false
			commandDepth++
			resumeDoubleDepths = append(resumeDoubleDepths, commandDepth)
			offset += 2
			continue
		}
		if !inSingle && !inDouble && commandDepth > 0 && line[offset] == '(' {
			commandDepth++
			offset++
			continue
		}
		if !inSingle && !inDouble && commandDepth > 0 && line[offset] == ')' {
			closingDepth := commandDepth
			commandDepth--
			if len(resumeDoubleDepths) > 0 && resumeDoubleDepths[len(resumeDoubleDepths)-1] == closingDepth {
				inDouble = true
				resumeDoubleDepths = resumeDoubleDepths[:len(resumeDoubleDepths)-1]
			}
			offset++
			continue
		}
		if !inSingle && !inDouble && line[offset] == '(' && line[offset+1] == '(' {
			arithmeticDepth++
			offset += 2
			continue
		}
		if !inSingle && !inDouble && arithmeticDepth > 0 && line[offset] == ')' && line[offset+1] == ')' {
			arithmeticDepth--
			offset += 2
			continue
		}
		if inSingle || inDouble || arithmeticDepth > 0 || line[offset] != '<' || line[offset+1] != '<' || offset+2 < len(line) && line[offset+2] == '<' {
			offset++
			continue
		}
		heredoc, next, found := parseShellHeredoc(line, offset+2)
		if found {
			heredocs = append(heredocs, heredoc)
			offset = next
			continue
		}
		offset += 2
	}
	return heredocs
}

func shellCommentStart(line string, state *shellLexState) int {
	if state.Quote == 0 && !state.WordContinues {
		state.InWord = false
	}
	state.WordContinues = false
	for offset := 0; offset < len(line); offset++ {
		current := line[offset]
		if state.Quote == '\'' {
			if current == '\'' {
				state.Quote = 0
			}
			continue
		}
		if state.Quote == '"' {
			if current == '\\' {
				offset++
				continue
			}
			if current == '"' {
				state.Quote = 0
				continue
			}
			if current == '$' && offset+1 < len(line) && line[offset+1] == '(' {
				state.Quote = 0
				state.CommandDepth++
				state.ResumeDoubleDepths = append(state.ResumeDoubleDepths, state.CommandDepth)
				state.InWord = false
				offset++
				continue
			}
			if current == '`' {
				state.Quote = 0
				state.BacktickDepth++
				state.BacktickResumeDoubleDepths = append(state.BacktickResumeDoubleDepths, state.BacktickDepth)
				state.InWord = false
			}
			continue
		}
		if current == '\\' && offset+1 < len(line) && line[offset+1] == '`' && state.BacktickDepth > 0 {
			if state.BacktickDepth == 1 {
				state.BacktickDepth++
				state.InWord = false
			} else {
				state.BacktickDepth--
				state.InWord = true
			}
			offset++
			continue
		}
		if current == '\\' {
			state.InWord = true
			if offset+1 == len(line) {
				state.WordContinues = true
				continue
			}
			offset++
			continue
		}
		if current == '\'' || current == '"' {
			state.Quote = current
			state.InWord = true
			continue
		}
		if current == '$' && offset+1 < len(line) && line[offset+1] == '(' {
			state.CommandDepth++
			state.InWord = false
			offset++
			continue
		}
		if current == '`' {
			if state.BacktickDepth > 0 {
				closingDepth := state.BacktickDepth
				state.BacktickDepth--
				state.InWord = true
				if len(state.BacktickResumeDoubleDepths) > 0 && state.BacktickResumeDoubleDepths[len(state.BacktickResumeDoubleDepths)-1] == closingDepth {
					state.Quote = '"'
					state.BacktickResumeDoubleDepths = state.BacktickResumeDoubleDepths[:len(state.BacktickResumeDoubleDepths)-1]
				}
			} else {
				state.BacktickDepth = 1
				state.InWord = false
			}
			continue
		}
		if state.CommandDepth > 0 && current == '(' {
			state.CommandDepth++
			continue
		}
		if state.CommandDepth > 0 && current == ')' {
			closingDepth := state.CommandDepth
			state.CommandDepth--
			state.InWord = true
			if len(state.ResumeDoubleDepths) > 0 && state.ResumeDoubleDepths[len(state.ResumeDoubleDepths)-1] == closingDepth {
				state.Quote = '"'
				state.ResumeDoubleDepths = state.ResumeDoubleDepths[:len(state.ResumeDoubleDepths)-1]
			}
			continue
		}
		if current == '#' && !state.InWord {
			state.InWord = false
			state.WordContinues = false
			return offset
		}
		if current == ' ' || current == '\t' || strings.ContainsRune(";|&()<>", rune(current)) {
			state.InWord = false
			continue
		}
		state.InWord = true
	}
	return -1
}

func parseShellHeredoc(line string, offset int) (shellHeredoc, int, bool) {
	for offset < len(line) && (line[offset] == ' ' || line[offset] == '\t') {
		offset++
	}
	stripTabs := offset < len(line) && line[offset] == '-'
	if stripTabs {
		offset++
	}
	for offset < len(line) && (line[offset] == ' ' || line[offset] == '\t') {
		offset++
	}
	start := offset
	var marker strings.Builder
	quote := byte(0)
	for offset < len(line) {
		current := line[offset]
		if quote != 0 {
			if current == quote {
				quote = 0
				offset++
				continue
			}
			if current == '\\' && quote == '"' && offset+1 < len(line) {
				offset++
				current = line[offset]
			}
			marker.WriteByte(current)
			offset++
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			offset++
			continue
		}
		if current == '\\' && offset+1 < len(line) {
			offset++
			marker.WriteByte(line[offset])
			offset++
			continue
		}
		if current == ' ' || current == '\t' || strings.ContainsRune(";|&<>()", rune(current)) {
			break
		}
		marker.WriteByte(current)
		offset++
	}
	if quote != 0 || offset == start || marker.Len() == 0 {
		return shellHeredoc{}, offset, false
	}
	return shellHeredoc{Marker: marker.String(), StripTabs: stripTabs}, offset, true
}

func hashCommentStart(line string) int {
	inSingle, inDouble := false, false
	for idx := 0; idx < len(line); idx++ {
		if line[idx] == '\\' {
			idx++
			continue
		}
		if line[idx] == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if line[idx] == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if line[idx] == '#' && !inSingle && !inDouble {
			return idx
		}
	}
	return -1
}

func markupComments(content []byte) []comment {
	return markupCommentsForPath("document.md", content)
}

func markupCommentsForPath(path string, content []byte) []comment {
	var comments []comment
	fenceMarker := byte(0)
	fenceLength := 0
	fenceBlockquoteDepth := 0
	fenceListIndent := 0
	inComment := false
	inCDATA := false
	inlineCodeTicks := 0
	listIndent := 0
	commentLine := 0
	var commentText strings.Builder
	line := 1
	for offset := 0; offset < len(content); {
		end := bytes.IndexByte(content[offset:], '\n')
		if end < 0 {
			end = len(content) - offset
		}
		current := content[offset : offset+end]
		lineContentOffset := offset
		isMarkdown := filepath.Ext(path) == ".md" || filepath.Ext(path) == ".markdown"
		blockquoteDepth := 0
		if isMarkdown {
			_, blockquoteDepth = markdownBlockquoteLine(current)
			originalLength := len(current)
			if fenceMarker != 0 {
				fenceContent, inside := markdownFenceContainerLine(current, fenceBlockquoteDepth, fenceListIndent)
				if inside {
					current = fenceContent
				} else {
					fenceMarker, fenceLength = 0, 0
					fenceBlockquoteDepth = 0
					fenceListIndent = 0
					current, listIndent = markdownContainerLine(current, listIndent)
				}
			} else {
				current, listIndent = markdownContainerLine(current, listIndent)
			}
			lineContentOffset += originalLength - len(current)
		}
		marker, length, isFence := markdownFence(current)
		if isMarkdown && fenceMarker == 0 && isFence {
			fenceMarker, fenceLength = marker, length
			fenceBlockquoteDepth = blockquoteDepth
			fenceListIndent = listIndent
		} else if isMarkdown && fenceMarker != 0 && isFence && marker == fenceMarker && length >= fenceLength && fenceCloseWhitespace(current, length) {
			fenceMarker, fenceLength = 0, 0
			fenceBlockquoteDepth = 0
			fenceListIndent = 0
		} else if fenceMarker == 0 && !(isMarkdown && markdownIndentedCode(current)) {
			remaining := current
			for len(remaining) > 0 {
				if inComment {
					close := bytes.Index(remaining, []byte("-->"))
					if close < 0 {
						commentText.Write(remaining)
						commentText.WriteByte('\n')
						break
					}
					commentText.Write(remaining[:close])
					comments = append(comments, comment{Line: commentLine, Text: commentText.String()})
					commentText.Reset()
					inComment = false
					remaining = remaining[close+3:]
					continue
				}
				if inCDATA {
					close := bytes.Index(remaining, []byte("]]>"))
					if close < 0 {
						break
					}
					remaining = remaining[close+3:]
					inCDATA = false
					continue
				}
				if inlineCodeTicks != 0 {
					close := matchingBacktickRun(remaining, inlineCodeTicks)
					if close < 0 {
						break
					}
					remaining = remaining[close+inlineCodeTicks:]
					inlineCodeTicks = 0
					continue
				}
				if bytes.HasPrefix(remaining, []byte("<![CDATA[")) {
					inCDATA = true
					remaining = remaining[len("<![CDATA["):]
					continue
				}
				if isMarkdown && remaining[0] == '`' {
					runLength := backtickRunLength(remaining)
					absoluteOffset := lineContentOffset + len(current) - len(remaining)
					paragraph := markdownParagraphRemainder(content[absoluteOffset+runLength:], listIndent)
					if escapedAt(content, absoluteOffset) || matchingBacktickRun(paragraph, runLength) < 0 {
						remaining = remaining[runLength:]
						continue
					}
					inlineCodeTicks = runLength
					remaining = remaining[runLength:]
					continue
				}
				if !bytes.HasPrefix(remaining, []byte("<!--")) {
					remaining = remaining[1:]
					continue
				}
				remaining = remaining[4:]
				inComment = true
				commentLine = line
			}
		}
		offset += end + 1
		line++
	}
	return comments
}

func markdownParagraphRemainder(content []byte, listIndent int) []byte {
	firstNewline := bytes.IndexByte(content, '\n')
	if firstNewline < 0 {
		return content
	}
	for lineStart := firstNewline + 1; lineStart < len(content); {
		lineEnd := bytes.IndexByte(content[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(content) - lineStart
		}
		line := content[lineStart : lineStart+lineEnd]
		line, listIndent = markdownContainerLine(line, listIndent)
		if len(bytes.TrimSpace(line)) == 0 || markdownCommentBlockStart(line) {
			return content[:lineStart]
		}
		lineStart += lineEnd + 1
	}
	return content
}

func markdownCommentBlockStart(line []byte) bool {
	content := markdownBlockquoteContent(line)
	indentBytes, indentColumns := markdownIndent(content)
	return indentColumns <= 3 && bytes.HasPrefix(content[indentBytes:], []byte("<!--"))
}

func markdownIndentedCode(line []byte) bool {
	content := markdownBlockquoteContent(line)
	_, columns := markdownIndent(content)
	return columns >= 4
}

func markdownContainerLine(line []byte, activeListIndent int) ([]byte, int) {
	content := markdownBlockquoteContent(line)
	if itemContent, contentIndent, found := markdownListItem(content); found {
		return itemContent, contentIndent
	}
	indentBytes, indentColumns := markdownIndent(content)
	if activeListIndent > 0 && indentColumns >= activeListIndent {
		return stripMarkdownIndent(content, activeListIndent), activeListIndent
	}
	if len(bytes.TrimSpace(content)) != 0 && indentBytes == 0 {
		activeListIndent = 0
	}
	return content, activeListIndent
}

func markdownFenceContainerLine(line []byte, blockquoteDepth, listIndent int) ([]byte, bool) {
	content, found := stripMarkdownBlockquotes(line, blockquoteDepth)
	if !found {
		return nil, false
	}
	if listIndent == 0 {
		return content, true
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return content, true
	}
	_, indentColumns := markdownIndent(content)
	if indentColumns < listIndent {
		return nil, false
	}
	return stripMarkdownIndent(content, listIndent), true
}

func stripMarkdownBlockquotes(line []byte, expectedDepth int) ([]byte, bool) {
	for range expectedDepth {
		indentBytes, indentColumns := markdownIndent(line)
		if indentColumns > 3 || indentBytes == len(line) || line[indentBytes] != '>' {
			return nil, false
		}
		line = line[indentBytes+1:]
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			line = line[1:]
		}
	}
	return line, true
}

func markdownListItem(line []byte) ([]byte, int, bool) {
	indentBytes, indentColumns := markdownIndent(line)
	if indentColumns > 3 || indentBytes == len(line) {
		return nil, 0, false
	}
	markerEnd := indentBytes
	if line[markerEnd] == '-' || line[markerEnd] == '+' || line[markerEnd] == '*' {
		markerEnd++
	} else {
		for markerEnd < len(line) && markerEnd-indentBytes < 9 && line[markerEnd] >= '0' && line[markerEnd] <= '9' {
			markerEnd++
		}
		if markerEnd == indentBytes || markerEnd == len(line) || line[markerEnd] != '.' && line[markerEnd] != ')' {
			return nil, 0, false
		}
		markerEnd++
	}
	if markerEnd == len(line) || line[markerEnd] != ' ' && line[markerEnd] != '\t' {
		return nil, 0, false
	}
	contentStart := markerEnd
	spaces := 0
	for contentStart < len(line) && spaces < 5 && (line[contentStart] == ' ' || line[contentStart] == '\t') {
		if line[contentStart] == '\t' {
			spaces += 4 - (indentColumns+markerEnd-indentBytes+spaces)%4
		} else {
			spaces++
		}
		contentStart++
	}
	if spaces == 0 {
		return nil, 0, false
	}
	if spaces > 4 {
		spaces = 1
		contentStart = markerEnd + 1
	}
	contentIndent := indentColumns + markerEnd - indentBytes + spaces
	return line[contentStart:], contentIndent, true
}

func stripMarkdownIndent(line []byte, targetColumns int) []byte {
	columns := 0
	offset := 0
	for offset < len(line) && columns < targetColumns {
		if line[offset] == ' ' {
			columns++
		} else if line[offset] == '\t' {
			columns += 4 - columns%4
		} else {
			break
		}
		offset++
	}
	return line[offset:]
}

func markdownBlockquoteContent(line []byte) []byte {
	content, _ := markdownBlockquoteLine(line)
	return content
}

func markdownBlockquoteLine(line []byte) ([]byte, int) {
	depth := 0
	for {
		indentBytes, indentColumns := markdownIndent(line)
		if indentColumns > 3 || indentBytes == len(line) || line[indentBytes] != '>' {
			return line, depth
		}
		depth++
		line = line[indentBytes+1:]
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			line = line[1:]
		}
	}
}

func markdownIndent(line []byte) (int, int) {
	columns := 0
	offset := 0
	for offset < len(line) {
		switch line[offset] {
		case ' ':
			columns++
		case '\t':
			columns += 4 - columns%4
		default:
			return offset, columns
		}
		offset++
	}
	return offset, columns
}

func backtickRunLength(content []byte) int {
	length := 0
	for length < len(content) && content[length] == '`' {
		length++
	}
	return length
}

func matchingBacktickRun(content []byte, expectedLength int) int {
	for offset := 0; offset < len(content); {
		if content[offset] != '`' {
			offset++
			continue
		}
		length := backtickRunLength(content[offset:])
		if length == expectedLength {
			return offset
		}
		offset += length
	}
	return -1
}

func fenceCloseWhitespace(line []byte, length int) bool {
	indent := len(line) - len(bytes.TrimLeft(line, " "))
	return strings.TrimSpace(string(line[indent+length:])) == ""
}

func markdownFence(line []byte) (byte, int, bool) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	if indent >= 4 || indent == len(line) {
		return 0, 0, false
	}
	marker := line[indent]
	if marker != '`' && marker != '~' {
		return 0, 0, false
	}
	length := 0
	for indent+length < len(line) && line[indent+length] == marker {
		length++
	}
	return marker, length, length >= 3
}

func sqlComments(content []byte) []comment {
	var comments []comment
	line := 1
	for offset := 0; offset < len(content); {
		if content[offset] == '\n' {
			line++
			offset++
			continue
		}
		if content[offset] == '\'' || content[offset] == '"' {
			next := skipQuoted(content, offset, content[offset])
			line += bytes.Count(content[offset:next], []byte("\n"))
			offset = next
			continue
		}
		if content[offset] == '$' {
			delimiter, found := sqlDollarDelimiter(content[offset:])
			if found {
				end := bytes.Index(content[offset+len(delimiter):], []byte(delimiter))
				if end >= 0 {
					next := offset + len(delimiter) + end + len(delimiter)
					line += bytes.Count(content[offset:next], []byte("\n"))
					offset = next
					continue
				}
			}
		}
		if offset+1 < len(content) && content[offset] == '/' && content[offset+1] == '*' {
			endLength := blockCommentEnd(content, offset+2, true)
			end := offset + 2 + endLength
			if end+1 >= len(content) || content[end] != '*' || content[end+1] != '/' {
				comments = append(comments, comment{Line: line, Text: string(content[offset+2:])})
				return comments
			}
			body := content[offset+2 : end]
			comments = append(comments, comment{Line: line, Text: string(body)})
			line += bytes.Count(body, []byte("\n"))
			offset = end + 2
			continue
		}
		if offset+1 >= len(content) || content[offset] != '-' || content[offset+1] != '-' {
			offset++
			continue
		}
		end := bytes.IndexByte(content[offset:], '\n')
		if end < 0 {
			end = len(content) - offset
		}
		comments = append(comments, comment{Line: line, Text: string(content[offset+2 : offset+end])})
		offset += end
	}
	return comments
}

func iniComments(content []byte) []comment {
	var comments []comment
	for idx, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if len(trimmed) > 0 && (trimmed[0] == '#' || trimmed[0] == ';') {
			comments = append(comments, comment{Line: idx + 1, Text: trimmed[1:]})
		}
	}
	return comments
}

func sqlDollarDelimiter(content []byte) (string, bool) {
	idx := 1
	for idx < len(content) && (content[idx] == '_' || content[idx] >= 'a' && content[idx] <= 'z' || content[idx] >= 'A' && content[idx] <= 'Z' || content[idx] >= '0' && content[idx] <= '9') {
		idx++
	}
	if idx >= len(content) || content[idx] != '$' {
		return "", false
	}
	return string(content[:idx+1]), true
}

func isSeparator(text string) bool {
	text = normalizedCommentText(text)
	if len(text) < 3 || !strings.ContainsRune("+-=*_~/#", rune(text[0])) {
		return false
	}
	for idx := 1; idx < len(text); idx++ {
		if text[idx] != text[0] {
			return false
		}
	}
	return true
}

func isSetextUnderline(commentLines []comment, commentIndex int) bool {
	if commentIndex == 0 || commentLines[commentIndex-1].Line+1 != commentLines[commentIndex].Line {
		return false
	}
	text := normalizedCommentText(commentLines[commentIndex].Text)
	if text[0] != '-' && text[0] != '=' {
		return false
	}
	previous := normalizedCommentText(commentLines[commentIndex-1].Text)
	return previous != "" && !isSeparator(previous)
}

func normalizedCommentText(text string) string {
	return strings.TrimSpace(text)
}

func goCommentFindings(path string, content []byte) ([]finding, error) {
	return goCommentFindingsWithContext(path, content, goCommentPackageContext{})
}

func goCommentFindingsWithContext(path string, content []byte, packageContext goCommentPackageContext) ([]finding, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse Go file %s: %w", path, err)
	}

	selections := map[*ast.CommentGroup]map[string]struct{}{}
	exportDirectiveGroups := map[*ast.CommentGroup]string{}
	hasCgoImport := slices.ContainsFunc(file.Imports, func(importSpec *ast.ImportSpec) bool {
		return importSpec.Path != nil && importSpec.Path.Value == `"C"`
	})
	addSelection := func(group *ast.CommentGroup, allowedWords ...string) {
		if group == nil {
			return
		}
		selection, found := selections[group]
		if !found {
			selection = map[string]struct{}{}
			selections[group] = selection
		}
		for _, word := range allowedWords {
			selection[word] = struct{}{}
		}
	}
	addBodySelections := func(body *ast.BlockStmt) {
		if body == nil {
			return
		}
		excluded := goCommentExcludedBodyGroups(body, file.Comments, packageContext)
		allowedWords := goCommentBodyAllowedWords(body)
		for _, group := range file.Comments {
			if group.Pos() > body.Lbrace && group.End() < body.Rbrace {
				if _, found := excluded[group]; found {
					continue
				}
				if words, found := allowedWords[group]; found {
					addSelection(group, words...)
					continue
				}
				addSelection(group)
			}
		}
	}

	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			addSelection(decl.Doc, decl.Name.Name)
			if hasCgoImport {
				exportName, found := goCommentExportDirectiveName(decl.Doc)
				if found && exportName == decl.Name.Name {
					exportDirectiveGroups[decl.Doc] = exportName
				}
			}
			addBodySelections(decl.Body)
		case *ast.GenDecl:
			if decl.Tok != token.VAR {
				continue
			}
			var names []string
			for _, spec := range decl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range valueSpec.Names {
					names = append(names, name.Name)
				}
				for _, name := range valueSpec.Names {
					addSelection(valueSpec.Doc, name.Name)
				}
			}
			addSelection(decl.Doc, names...)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		funcLit, ok := node.(*ast.FuncLit)
		if ok {
			addBodySelections(funcLit.Body)
		}
		return true
	})

	findings := make([]finding, 0, len(selections))
	for group, allowedWords := range selections {
		exportDirective := exportDirectiveGroups[group]
		word, line, exact, ok := goCommentOpening(fileSet, content, group, exportDirective)
		if !ok || exact && isAllowedGoCommentOpening(word, allowedWords) {
			continue
		}
		findings = append(findings, finding{
			Path:    path,
			Line:    line,
			Message: "comment sentence starts with lowercase word",
		})
	}
	sort.Slice(findings, func(left, right int) bool {
		if findings[left].Path == findings[right].Path {
			if findings[left].Line == findings[right].Line {
				return findings[left].Message < findings[right].Message
			}
			return findings[left].Line < findings[right].Line
		}
		return findings[left].Path < findings[right].Path
	})
	return findings, nil
}

func goCommentExcludedBodyGroups(body *ast.BlockStmt, comments []*ast.CommentGroup, packageContext goCommentPackageContext) map[*ast.CommentGroup]struct{} {
	excluded := map[*ast.CommentGroup]struct{}{}
	addGroup := func(group *ast.CommentGroup) {
		if group != nil {
			excluded[group] = struct{}{}
		}
	}
	addRange := func(start, end token.Pos) {
		for _, comment := range comments {
			if comment.Pos() >= start && comment.End() <= end {
				addGroup(comment)
			}
		}
	}
	addTypeSyntax := func(node ast.Node) {
		if node == nil {
			return
		}
		addRange(node.Pos(), node.End())
		ast.Inspect(node, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.ArrayType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType,
				*ast.MapType, *ast.StructType:
				addRange(node.Pos(), node.End())
			case *ast.Field:
				addGroup(node.Doc)
				addGroup(node.Comment)
			}
			return true
		})
	}
	ast.Inspect(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ValueSpec:
			if node.Type != nil {
				start := node.Type.Pos()
				if len(node.Names) > 0 {
					start = node.Names[0].End()
				}
				addRange(start, node.Type.End())
				addTypeSyntax(node.Type)
			}
		case *ast.CompositeLit:
			if node.Type != nil {
				addRange(node.Type.Pos(), node.Lbrace)
				addTypeSyntax(node.Type)
			}
		case *ast.FuncLit:
			addRange(node.Type.Pos(), node.Body.Pos())
			addTypeSyntax(node.Type)
		case *ast.TypeAssertExpr:
			if node.Type != nil {
				addRange(node.Lparen, node.End())
			} else {
				addRange(node.Lparen, node.End())
			}
			addTypeSyntax(node.Type)
		case *ast.TypeSwitchStmt:
			for _, statement := range node.Body.List {
				clause, ok := statement.(*ast.CaseClause)
				if ok {
					addRange(clause.Case, clause.Colon)
				}
			}
		case *ast.CallExpr:
			if function, ok := node.Fun.(*ast.FuncLit); ok {
				addRange(function.Type.Pos(), function.Body.Pos())
			} else if goCommentIsConversionType(node.Fun) {
				addRange(node.Fun.Pos(), node.Lparen)
				addTypeSyntax(node.Fun)
			}
			fun := node.Fun
			for {
				parenthesized, ok := fun.(*ast.ParenExpr)
				if !ok {
					break
				}
				fun = parenthesized.X
			}
			if ident, ok := fun.(*ast.Ident); ok {
				_, shadowed := packageContext.ShadowedBuiltins[ident.Name]
				if ident.Obj == nil && !shadowed &&
					(ident.Name == "make" || ident.Name == "new") && len(node.Args) > 0 {
					addRange(node.Lparen, node.Args[0].End())
					addTypeSyntax(node.Args[0])
				}
			}
		case *ast.Field:
			addGroup(node.Doc)
			addGroup(node.Comment)
			addTypeSyntax(node.Type)
		}
		declStmt, ok := node.(*ast.DeclStmt)
		if !ok {
			return true
		}
		genDecl, ok := declStmt.Decl.(*ast.GenDecl)
		if !ok {
			return true
		}
		if genDecl.Tok == token.VAR {
			return true
		}
		if genDecl.Tok != token.TYPE && genDecl.Tok != token.CONST {
			return true
		}
		addGroup(genDecl.Doc)
		addRange(genDecl.Pos(), genDecl.End())
		for _, spec := range genDecl.Specs {
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				addGroup(spec.Doc)
				addGroup(spec.Comment)
			case *ast.ValueSpec:
				addGroup(spec.Doc)
				addGroup(spec.Comment)
			}
		}
		return true
	})
	return excluded
}

func goCommentIsConversionType(expression ast.Expr) bool {
	switch expression := expression.(type) {
	case *ast.ArrayType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType,
		*ast.MapType, *ast.StructType:
		return true
	case *ast.StarExpr:
		return goCommentIsConversionType(expression.X)
	case *ast.ParenExpr:
		return goCommentIsConversionType(expression.X)
	default:
		return false
	}
}

func goCommentBodyAllowedWords(body *ast.BlockStmt) map[*ast.CommentGroup][]string {
	allowed := map[*ast.CommentGroup][]string{}
	addGroup := func(group *ast.CommentGroup, names []string) {
		if group != nil {
			allowed[group] = append(allowed[group], names...)
		}
	}
	ast.Inspect(body, func(node ast.Node) bool {
		declStmt, ok := node.(*ast.DeclStmt)
		if !ok {
			return true
		}
		genDecl, ok := declStmt.Decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			return true
		}
		var names []string
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			var specNames []string
			for _, name := range valueSpec.Names {
				specNames = append(specNames, name.Name)
				names = append(names, name.Name)
			}
			addGroup(valueSpec.Doc, specNames)
		}
		addGroup(genDecl.Doc, names)
		return true
	})
	return allowed
}

type goCommentPhysicalLine struct {
	Text            string
	RawText         string
	Line            int
	Directive       bool
	Block           bool
	BlockBaseIndent int
	BlockEnd        bool
}

func goCommentOpening(fileSet *token.FileSet, content []byte, group *ast.CommentGroup, exportDirectiveName string) (string, int, bool, bool) {
	if group == nil || len(group.List) == 0 {
		return "", 0, false, false
	}

	lines := goCommentPhysicalLines(fileSet, content, group, exportDirectiveName)
	isBlock := strings.HasPrefix(group.List[0].Text, "/*")
	paragraph := make([]goCommentPhysicalLine, 0, len(lines))
	started := false
	var fence *goCommentFence
	for _, physicalLine := range lines {
		rawLine := physicalLine.Text
		line := strings.TrimSpace(rawLine)
		if isBlock && strings.HasPrefix(line, "*") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		}
		if fence != nil {
			paragraph = append(paragraph, goCommentPhysicalLine{Text: line, RawText: rawLine, Line: physicalLine.Line, Directive: physicalLine.Directive, Block: physicalLine.Block, BlockBaseIndent: physicalLine.BlockBaseIndent, BlockEnd: physicalLine.BlockEnd})
			if goCommentIsFenceClose(rawLine, *fence) {
				fence = nil
			}
			continue
		}
		if line == "" {
			if started {
				if physicalLine.Block && physicalLine.BlockEnd {
					continue
				}
				break
			}
			continue
		}
		started = true
		paragraph = append(paragraph, goCommentPhysicalLine{
			Text:            line,
			RawText:         rawLine,
			Line:            physicalLine.Line,
			Directive:       physicalLine.Directive,
			Block:           physicalLine.Block,
			BlockBaseIndent: physicalLine.BlockBaseIndent,
			BlockEnd:        physicalLine.BlockEnd,
		})
		if marker, found := goCommentFenceMarker(rawLine); found {
			fence = &marker
		}
	}
	if len(paragraph) == 0 {
		return "", 0, false, false
	}
	paragraph = goCommentPreparePhysicalParagraph(paragraph)
	for len(paragraph) > 1 {
		_, _, _, _, terminated := goCommentNextSentence(paragraph[0].Text)
		if terminated || !goCommentIsExcludedPhysicalLine(paragraph[0]) {
			break
		}
		paragraph = paragraph[1:]
	}
	lineStarts := make([]int, len(paragraph))
	openingBuilder := strings.Builder{}
	for idx, physicalLine := range paragraph {
		if idx != 0 {
			openingBuilder.WriteByte(' ')
		}
		lineStarts[idx] = openingBuilder.Len()
		openingBuilder.WriteString(physicalLine.Text)
	}
	opening := openingBuilder.String()
	if !goCommentIsSpacedSingleWordList(opening) && goCommentLooksLikeWholeLineCode(opening) {
		return "", 0, false, false
	}
	remaining := opening
	remainingOffset := 0
	for {
		sentence, rest, sentenceOffset, restOffset, ok := goCommentNextSentence(remaining)
		if !ok {
			return "", 0, false, false
		}
		if suffixOffset, found := goCommentStructuralSemicolonSuffix(sentence); found {
			remaining = sentence[suffixOffset:] + rest
			remainingOffset += sentenceOffset + suffixOffset
			continue
		}
		if prefix, found := goCommentURLPrefix(sentence); found {
			remove := sentenceOffset + prefix
			remaining = remaining[remove:]
			trimmed := strings.TrimLeft(remaining, " \t")
			remainingOffset += remove + len(remaining) - len(trimmed)
			remaining = trimmed
			continue
		}
		if goCommentStartsWithDirectiveText(sentence) || goCommentStartsWithTodo(sentence) ||
			goCommentLooksLikeMarkdownLink(sentence) && !goCommentMarkdownLinkCrossesPhysicalLine(sentence, remainingOffset+sentenceOffset, lineStarts) ||
			goCommentIsSymbolOnlyDiagram(sentence) || goCommentLooksLikeCode(sentence) {
			remaining = rest
			remainingOffset += restOffset
			continue
		}
		word, wordOffset, exact, ok := goCommentOpeningWord(sentence)
		if !ok {
			if goCommentIsExcludedOpening(sentence) || goCommentStartsWithNumericFragment(sentence) || goCommentIsPunctuationOnlyOpening(sentence) {
				remaining = rest
				remainingOffset += restOffset
				continue
			}
			return "", 0, false, false
		}
		openingOffset := remainingOffset + sentenceOffset + wordOffset
		lineIndex := 0
		for idx := 1; idx < len(lineStarts) && lineStarts[idx] <= openingOffset; idx++ {
			lineIndex = idx
		}
		return word, paragraph[lineIndex].Line, exact, true
	}
}

func goCommentMarkdownLinkCrossesPhysicalLine(sentence string, offset int, lineStarts []int) bool {
	start, end, found := goCommentMarkdownAngleDestinationSpan(sentence)
	if !found {
		return false
	}
	for _, lineStart := range lineStarts {
		if offset+start < lineStart && lineStart <= offset+end {
			return true
		}
	}
	return false
}

func goCommentMarkdownAngleDestinationSpan(sentence string) (int, int, bool) {
	text := strings.TrimSpace(goCommentTrimSentenceTerminal(sentence))
	base := len(sentence) - len(strings.TrimLeft(sentence, " \t"))
	labelStart := 0
	if strings.HasPrefix(text, "!") {
		labelStart++
	}
	if labelStart >= len(text) || text[labelStart] != '[' {
		return 0, 0, false
	}
	close := goCommentBalancedDelimiterEnd(text, labelStart, '[', ']')
	if close < 0 || close+1 >= len(text) {
		return 0, 0, false
	}
	start := close + 1
	if text[start] == '(' {
		start++
	} else if text[start] != ':' {
		return 0, 0, false
	} else {
		start++
	}
	for start < len(text) && (text[start] == ' ' || text[start] == '\t') {
		start++
	}
	if start >= len(text) || text[start] != '<' {
		return 0, 0, false
	}
	for end := start + 1; end < len(text); end++ {
		if text[end] == '\\' {
			end++
			continue
		}
		if text[end] == '>' {
			return base + start, base + end, true
		}
	}
	return 0, 0, false
}

func goCommentStructuralSemicolonSuffix(sentence string) (int, bool) {
	for _, current := range goCommentScanTokens(sentence) {
		if current.Kind != token.SEMICOLON || current.Literal != ";" {
			continue
		}
		prefix := strings.TrimSpace(sentence[:current.Start])
		suffixStart := current.End
		for suffixStart < len(sentence) && (sentence[suffixStart] == ' ' || sentence[suffixStart] == '\t') {
			suffixStart++
		}
		if prefix != "" && suffixStart < len(sentence) && goCommentLooksLikeCode(prefix+".") {
			return suffixStart, true
		}
	}
	return 0, false
}

func goCommentPreparePhysicalParagraph(lines []goCommentPhysicalLine) []goCommentPhysicalLine {
	prepared := make([]goCommentPhysicalLine, 0, len(lines))
	for idx := 0; idx < len(lines); {
		if lines[idx].Directive {
			idx++
			continue
		}
		if goCommentIsIndentedCodeLine(lines[idx]) {
			if idx > 0 && len(prepared) > 0 {
				prepared = append(prepared, lines[idx])
			}
			idx++
			continue
		}
		text := strings.TrimSpace(lines[idx].Text)
		rawText := lines[idx].RawText
		if rawText == "" {
			rawText = lines[idx].Text
		}
		if fence, found := goCommentFenceMarker(rawText); found {
			idx++
			for idx < len(lines) {
				closeText := lines[idx].RawText
				if closeText == "" {
					closeText = lines[idx].Text
				}
				if goCommentIsFenceClose(closeText, fence) {
					idx++
					break
				}
				idx++
			}
			continue
		}
		if idx+1 < len(lines) && text != "" {
			underline := lines[idx+1].Text
			if lines[idx+1].RawText != "" {
				underline = lines[idx+1].RawText
			}
			if goCommentIsSetextUnderline(goCommentFenceLine(underline)) {
				idx += 2
				continue
			}
		}
		if end, remainder, found := goCommentMultilineCodeEnd(lines, idx); found {
			idx = end
			if remainder != nil {
				prepared = append(prepared, *remainder)
			}
			continue
		}
		prepared = append(prepared, lines[idx])
		idx++
	}
	return prepared
}

func goCommentMultilineCodeEnd(lines []goCommentPhysicalLine, start int) (int, *goCommentPhysicalLine, bool) {
	if start+1 >= len(lines) ||
		(!goCommentHasUnclosedDelimiter(lines[start].Text) && !goCommentHasContinuingToken(lines[start].Text)) {
		return 0, nil, false
	}
	var builder strings.Builder
	builder.WriteString(lines[start].Text)
	for end := start + 1; end < len(lines); end++ {
		line := lines[end]
		bestBoundary := 0
		for _, boundary := range goCommentTokenBoundaries(line.Text) {
			candidate := builder.String() + "\n" + line.Text[:boundary]
			if goCommentLooksLikeMultilineCode(candidate) {
				bestBoundary = boundary
			}
		}
		if bestBoundary != 0 {
			var remainder *goCommentPhysicalLine
			if rest := strings.TrimLeft(line.Text[bestBoundary:], " \t"); rest != "" {
				remainder = &goCommentPhysicalLine{
					Text:            rest,
					RawText:         rest,
					Line:            line.Line,
					Directive:       line.Directive,
					Block:           line.Block,
					BlockBaseIndent: line.BlockBaseIndent,
					BlockEnd:        line.BlockEnd,
				}
			}
			if remainder == nil && strings.HasSuffix(strings.TrimSpace(line.Text[:bestBoundary]), ".") {
				chainEnd := end + 1
				chainBuilder := builder.String() + "\n" + line.Text[:bestBoundary]
				for chainEnd < len(lines) {
					next := strings.TrimSpace(lines[chainEnd].Text)
					candidate := chainBuilder + next
					if next == "" || !goCommentLooksLikeMultilineCode(candidate) {
						break
					}
					chainBuilder = candidate
					chainEnd++
				}
				if chainEnd > end+1 {
					return chainEnd, nil, true
				}
			}
			return end + 1, remainder, true
		}
		builder.WriteByte('\n')
		builder.WriteString(line.Text)
	}
	return 0, nil, false
}

func goCommentTokenBoundaries(line string) []int {
	boundaries := make([]int, 0)
	for _, current := range goCommentScanTokens(line) {
		boundary := min(current.End, len(line))
		if boundary > 0 {
			boundaries = append(boundaries, boundary)
		}
	}
	return boundaries
}

func goCommentHasContinuingToken(line string) bool {
	tokens := goCommentScanTokens(line)
	if len(tokens) == 0 {
		return false
	}
	last := tokens[len(tokens)-1].Kind
	if last.IsOperator() {
		return last != token.INC && last != token.DEC
	}
	return slices.Contains([]token.Token{token.COMMA, token.PERIOD, token.COLON}, last)
}

func goCommentHasUnclosedDelimiter(line string) bool {
	stack := make([]token.Token, 0)
	for _, current := range goCommentScanTokens(line) {
		switch current.Kind {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			stack = append(stack, current.Kind)
		case token.RPAREN, token.RBRACK, token.RBRACE:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return len(stack) > 0
}

func goCommentLooksLikeMultilineCode(expression string) bool {
	trimmed := goCommentTrimSentenceTerminal(expression)
	if goCommentLooksLikeFileDeclarationCode(trimmed) {
		return true
	}
	if parsed, err := parser.ParseExpr(trimmed); err == nil {
		if _, composite := parsed.(*ast.CompositeLit); composite {
			return true
		}
		tokens := goCommentScanTokens(trimmed)
		return goCommentIsCodeExpression(parsed, tokens) || goCommentHasTightStructuredDelimiter(tokens)
	}
	if normalized := strings.ReplaceAll(trimmed, "\n", " "); normalized != trimmed {
		if parsed, err := parser.ParseExpr(normalized); err == nil {
			tokens := goCommentScanTokens(normalized)
			return goCommentIsCodeExpression(parsed, tokens) || goCommentHasTightStructuredDelimiter(tokens)
		}
	}
	source := "package p\nfunc snippet() {\n" + trimmed + "\n}\n"
	file, err := parser.ParseFile(token.NewFileSet(), "snippet.go", source, 0)
	if err != nil {
		return false
	}
	funcDecl := file.Decls[0].(*ast.FuncDecl)
	return goCommentAllStatementsCode(funcDecl.Body.List) || goCommentHasExplicitStatementBlock(funcDecl.Body.List)
}

type goCommentFence struct {
	Glyph  byte
	Length int
}

func goCommentFenceMarker(text string) (goCommentFence, bool) {
	text = goCommentFenceLine(text)
	_, indentColumns := goCommentIndent(text)
	if indentColumns >= 4 {
		return goCommentFence{}, false
	}
	indentBytes, _ := goCommentIndent(text)
	text = text[indentBytes:]
	if len(text) < 3 || (text[0] != '`' && text[0] != '~') {
		return goCommentFence{}, false
	}
	glyph := text[0]
	count := 0
	for count < len(text) && text[count] == glyph {
		count++
	}
	if count < 3 {
		return goCommentFence{}, false
	}
	if glyph == '`' {
		if strings.ContainsRune(text[count:], rune(glyph)) {
			return goCommentFence{}, false
		}
	}
	return goCommentFence{Glyph: glyph, Length: count}, true
}

func goCommentIsFenceClose(text string, fence goCommentFence) bool {
	text = goCommentFenceLine(text)
	_, indentColumns := goCommentIndent(text)
	if indentColumns >= 4 {
		return false
	}
	indentBytes, _ := goCommentIndent(text)
	text = text[indentBytes:]
	if len(text) < fence.Length || text[0] != fence.Glyph {
		return false
	}
	count := 0
	for count < len(text) && text[count] == fence.Glyph {
		count++
	}
	return count >= fence.Length && strings.TrimSpace(text[count:]) == ""
}

func goCommentIsSetextUnderline(text string) bool {
	indentBytes, indentColumns := goCommentIndent(text)
	if indentColumns > 3 {
		return false
	}
	text = strings.TrimSpace(text[indentBytes:])
	if text == "" || (text[0] != '-' && text[0] != '=') {
		return false
	}
	for idx := 1; idx < len(text); idx++ {
		if text[idx] != text[0] {
			return false
		}
	}
	return true
}

func goCommentIsIndentedCodeLine(line goCommentPhysicalLine) bool {
	text := line.RawText
	if text == "" {
		text = line.Text
	}
	indentBytes, columns := goCommentIndent(text)
	if line.Block {
		effectiveIndentColumns := columns
		if indentBytes < len(text) && text[indentBytes] == '*' &&
			(indentBytes+1 == len(text) || text[indentBytes+1] == ' ' || text[indentBytes+1] == '\t') {
			afterStar := text[indentBytes+1:]
			_, afterStarColumns := goCommentIndent(afterStar)
			effectiveIndentColumns += afterStarColumns
		}
		return effectiveIndentColumns-line.BlockBaseIndent >= 4
	}
	_, columns = goCommentIndent(goCommentFenceLine(text))
	return columns >= 4
}

func goCommentIndent(text string) (int, int) {
	bytesConsumed := 0
	columns := 0
	for bytesConsumed < len(text) {
		switch text[bytesConsumed] {
		case ' ':
			columns++
		case '\t':
			columns += 4 - columns%4
		default:
			return bytesConsumed, columns
		}
		bytesConsumed++
	}
	return bytesConsumed, columns
}

func goCommentFenceLine(text string) string {
	indentBytes, indentColumns := goCommentIndent(text)
	if indentColumns >= 4 || indentBytes == len(text) {
		return text
	}
	if text[indentBytes] != '*' || indentBytes+1 < len(text) && text[indentBytes+1] != ' ' && text[indentBytes+1] != '\t' {
		return text
	}
	return strings.TrimLeft(text[indentBytes+1:], " \t")
}

func goCommentIsExcludedPhysicalLine(line goCommentPhysicalLine) bool {
	text := strings.TrimSpace(line.Text)
	return line.Directive || goCommentStartsWithDirectiveText(text) || goCommentStartsWithTodo(text) ||
		goCommentLooksLikeURL(text) || goCommentLooksLikeMarkdownLink(text) ||
		goCommentLooksLikeWholeLineCode(text) || goCommentIsExcludedOpening(text) ||
		goCommentIsPunctuationOnlyOpening(text) || goCommentIsSymbolOnlyDiagram(text)
}

func goCommentLooksLikeMarkdownLink(text string) bool {
	text = strings.TrimSpace(goCommentTrimSentenceTerminal(text))
	labelStart := 0
	if strings.HasPrefix(text, "!") {
		labelStart = 1
	}
	if labelStart >= len(text) || text[labelStart] != '[' {
		return false
	}
	close := goCommentBalancedDelimiterEnd(text, labelStart, '[', ']')
	if close <= 0 || close+1 >= len(text) {
		return false
	}
	if text[close+1] == '(' {
		end := goCommentMarkdownLinkEnd(text, close+1)
		return end == len(text)-1 && goCommentLooksLikeMarkdownReference(text[close+2:end])
	}
	if text[close+1] == '[' {
		return goCommentBalancedDelimiterEnd(text, close+1, '[', ']') == len(text)-1
	}
	if text[close+1] == ':' {
		if labelStart != 0 {
			return false
		}
		return goCommentLooksLikeMarkdownReference(text[close+2:])
	}
	return false
}

func goCommentMarkdownLinkEnd(text string, start int) int {
	if start >= len(text) || text[start] != '(' {
		return -1
	}
	depth := 0
	for idx := start; idx < len(text); idx++ {
		if text[idx] == '\\' {
			idx++
			continue
		}
		if text[idx] == '<' && depth == 1 {
			for idx++; idx < len(text); idx++ {
				if text[idx] == '\\' {
					idx++
					continue
				}
				if text[idx] == '>' {
					break
				}
			}
			if idx == len(text) {
				return -1
			}
			continue
		}
		switch text[idx] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return idx
			}
		}
	}
	return -1
}

func goCommentLooksLikeMarkdownReference(text string) bool {
	text = strings.TrimSpace(text)
	prefix, found := goCommentURLPrefix(text)
	if !found {
		return goCommentLooksLikeMarkdownDestinationWithSuffix(text)
	}
	remainder := strings.TrimSpace(text[prefix:])
	if remainder == "" || goCommentIsURLPunctuation(remainder) {
		return true
	}
	if remainder[0] == '"' || remainder[0] == '\'' {
		quote := remainder[0]
		for idx := 1; idx < len(remainder); idx++ {
			if remainder[idx] == '\\' {
				idx++
				continue
			}
			if remainder[idx] == quote {
				return strings.TrimSpace(remainder[idx+1:]) == ""
			}
		}
	}
	if remainder[0] == '(' {
		return goCommentBalancedDelimiterEnd(remainder, 0, '(', ')') == len(remainder)-1
	}
	return false
}

func goCommentLooksLikeMarkdownDestinationWithSuffix(text string) bool {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<") {
		end := -1
		for idx := 1; idx < len(text); idx++ {
			if text[idx] == '\\' {
				idx++
				continue
			}
			if text[idx] == '>' {
				end = idx
				break
			}
		}
		if end < 0 || !goCommentLooksLikeMarkdownDestination(text[:end+1]) {
			return false
		}
		return goCommentLooksLikeMarkdownTitle(text[end+1:])
	}
	end := strings.IndexAny(text, " \t")
	if end < 0 {
		return goCommentLooksLikeMarkdownDestination(text)
	}
	if !goCommentLooksLikeMarkdownDestination(text[:end]) {
		return false
	}
	return goCommentLooksLikeMarkdownTitle(text[end:])
}

func goCommentLooksLikeMarkdownTitle(text string) bool {
	remainder := strings.TrimSpace(text)
	if remainder == "" {
		return true
	}
	if remainder[0] == '(' {
		end := goCommentBalancedDelimiterEnd(remainder, 0, '(', ')')
		if end != len(remainder)-1 {
			return false
		}
		for idx := 1; idx < end; idx++ {
			if remainder[idx] == '\\' {
				idx++
				continue
			}
			if remainder[idx] == '(' || remainder[idx] == ')' {
				return false
			}
		}
		return true
	}
	if remainder[0] != '"' && remainder[0] != '\'' {
		return false
	}
	quote := remainder[0]
	for idx := 1; idx < len(remainder); idx++ {
		if remainder[idx] == '\\' {
			idx++
			continue
		}
		if remainder[idx] == quote {
			return strings.TrimSpace(remainder[idx+1:]) == ""
		}
	}
	return false
}

func goCommentLooksLikeMarkdownDestination(text string) bool {
	if strings.ContainsAny(text, "\r\n") {
		return false
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<") {
		if len(text) < 2 || text[len(text)-1] != '>' {
			return false
		}
		for idx := 1; idx < len(text)-1; idx++ {
			switch text[idx] {
			case '<', '>':
				return false
			case '\n', '\r':
				return false
			case '\\':
				if idx+1 >= len(text)-1 {
					return false
				}
				idx++
			}
		}
		return true
	}
	if text != "" && goCommentIsASCIIDigits(text) {
		return false
	}
	depth := 0
	for idx := 0; idx < len(text); idx++ {
		value := text[idx]
		if value == '\\' {
			if idx+1 == len(text) || !goCommentIsMarkdownEscapablePunctuation(text[idx+1]) {
				return false
			}
			idx++
			continue
		}
		if value == ' ' || value == '\t' || value == '\r' || value == '\n' || value < 0x20 {
			return false
		}
		switch value {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return false
			}
			depth--
		}
	}
	return depth == 0
}

func goCommentIsMarkdownEscapablePunctuation(value byte) bool {
	return strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(value))
}

func goCommentBalancedDelimiterEnd(text string, start int, opening, closing byte) int {
	if start >= len(text) || text[start] != opening {
		return -1
	}
	depth := 0
	for idx := start; idx < len(text); idx++ {
		if text[idx] == '\\' {
			idx++
			continue
		}
		switch text[idx] {
		case opening:
			depth++
		case closing:
			depth--
			if depth == 0 {
				return idx
			}
		}
	}
	return -1
}

func goCommentIsSymbolOnlyDiagram(text string) bool {
	text = strings.TrimSpace(goCommentTrimSentenceTerminal(text))
	hasSymbol := false
	for _, value := range strings.TrimSpace(text) {
		if unicode.IsSpace(value) {
			continue
		}
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			return false
		}
		if !unicode.IsPunct(value) && !unicode.IsSymbol(value) {
			return false
		}
		hasSymbol = true
	}
	return hasSymbol
}

func goCommentPhysicalLines(fileSet *token.FileSet, content []byte, group *ast.CommentGroup, exportDirectiveName string) []goCommentPhysicalLine {
	lines := make([]goCommentPhysicalLine, 0)
	for _, comment := range group.List {
		line := fileSet.PositionFor(comment.Slash, false).Line
		text := comment.Text
		isBlock := strings.HasPrefix(text, "/*")
		blockBaseIndent := 0
		if isBlock {
			position := fileSet.PositionFor(comment.Slash, false)
			lineStart := position.Offset
			for lineStart > 0 && content[lineStart-1] != '\n' {
				lineStart--
			}
			_, blockBaseIndent = goCommentIndent(string(content[lineStart:position.Offset]))
		}
		exportDirective := false
		if exportDirectiveName != "" {
			name, found := goCommentExportDirectiveNameText(text)
			exportDirective = found && name == exportDirectiveName
		}
		directive := !isBlock && (goCommentIsDirectiveLine(text) || exportDirective)
		if isBlock {
			text = strings.TrimPrefix(text, "/*")
			text = strings.TrimSuffix(text, "*/")
		} else {
			if strings.HasPrefix(text, "///") || strings.HasPrefix(text, "//!") {
				text = text[3:]
			} else {
				text = strings.TrimPrefix(text, "//")
			}
		}
		physicalLines := strings.Split(text, "\n")
		for lineOffset, physicalText := range physicalLines {
			lines = append(lines, goCommentPhysicalLine{
				Text:            physicalText,
				RawText:         physicalText,
				Line:            line + lineOffset,
				Directive:       directive,
				Block:           isBlock,
				BlockBaseIndent: blockBaseIndent,
				BlockEnd:        isBlock && lineOffset == len(physicalLines)-1,
			})
		}
	}
	return lines
}

func goCommentExportDirectiveName(group *ast.CommentGroup) (string, bool) {
	if group == nil || len(group.List) == 0 {
		return "", false
	}
	for _, comment := range group.List {
		if name, found := goCommentExportDirectiveNameText(comment.Text); found {
			return name, true
		}
	}
	return "", false
}

func goCommentExportDirectiveNameText(text string) (string, bool) {
	if !strings.HasPrefix(text, "//export") ||
		(len(text) > len("//export") && text[len("//export")] != ' ' && text[len("//export")] != '\t') {
		return "", false
	}
	name := strings.TrimSpace(text[len("//export"):])
	if name == "" || strings.ContainsAny(name, " \t\r\n") {
		return "", false
	}
	return name, true
}

func goCommentIsDirectiveLine(text string) bool {
	for _, prefix := range []string{"//go:", "//lint:"} {
		if strings.HasPrefix(text, prefix) && goCommentDirectiveName(text[len(prefix):]) {
			return true
		}
	}
	return false
}

func goCommentDirectiveName(text string) bool {
	if text == "" || !goCommentIsASCIILowerOrDigit(text[0]) {
		return false
	}
	for idx := 1; idx < len(text); idx++ {
		if unicode.IsSpace(rune(text[idx])) || unicode.In(rune(text[idx]), unicode.Zs, unicode.Zl, unicode.Zp) {
			return true
		}
	}
	return true
}

func goCommentIsASCIILowerOrDigit(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func goCommentIsASCIIIdentifierStart(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func goCommentIsASCIIIdentifierPart(value byte) bool {
	return goCommentIsASCIIIdentifierStart(value) || value >= '0' && value <= '9' || value == '_'
}

func goCommentIsExcludedOpening(sentence string) bool {
	idx := 0
	for idx < len(sentence) && sentence[idx] >= '0' && sentence[idx] <= '9' {
		idx++
	}
	return idx > 0 && idx < len(sentence) && unicode.IsSpace(rune(sentence[idx]))
}

func goCommentStartsWithNumericFragment(sentence string) bool {
	if sentence == "" || sentence[0] < '0' || sentence[0] > '9' {
		return false
	}
	idx := 0
	for idx < len(sentence) && sentence[idx] >= '0' && sentence[idx] <= '9' {
		idx++
	}
	if idx < len(sentence) && sentence[idx] == ')' {
		return false
	}
	return !(idx < len(sentence) && sentence[idx] == '.' && idx+1 < len(sentence) && unicode.IsSpace(rune(sentence[idx+1])))
}

func goCommentStartsWithDirectiveText(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range []string{"nolint", "@param", "@return", "@see", "@file"} {
		if strings.HasPrefix(text, prefix) &&
			(goCommentWordBoundary(text, len(prefix)) || prefix == "@param" && len(text) > len(prefix) && text[len(prefix)] == '[') {
			return true
		}
	}
	return false
}

func goCommentStartsWithTodo(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return (strings.HasPrefix(text, "todo") && goCommentTodoBoundary(text, len("todo"))) || (strings.HasPrefix(text, "fixme") && goCommentTodoBoundary(text, len("fixme")))
}

func goCommentTodoBoundary(text string, offset int) bool {
	return goCommentWordBoundary(text, offset) || offset < len(text) && text[offset] == '('
}

func goCommentWordBoundary(text string, offset int) bool {
	return offset == len(text) || text[offset] == ':' || text[offset] == ' ' || text[offset] == '\t'
}

func goCommentNextSentence(line string) (string, string, int, int, bool) {
	var quote byte
	var smartQuote string
	for idx := 0; idx < len(line); idx++ {
		if quote != 0 {
			switch quote {
			case '`':
				if line[idx] == quote {
					quote = 0
				}
			default:
				if line[idx] == quote {
					quote = 0
				} else if line[idx] == '\\' {
					idx++
				}
			}
			continue
		}
		if smartQuote != "" {
			if strings.HasPrefix(line[idx:], smartQuote) {
				idx += len(smartQuote) - 1
				smartQuote = ""
				continue
			}
			if line[idx] == '.' || line[idx] == '?' || line[idx] == '!' {
				if end, ok := goCommentSentenceEnd(line, idx); ok {
					return goCommentSentenceParts(line, end)
				}
				continue
			}
			continue
		}
		if strings.HasPrefix(line[idx:], "“") {
			idx += len("“") - 1
			smartQuote = "”"
			continue
		}
		if strings.HasPrefix(line[idx:], "‘") {
			idx += len("‘") - 1
			smartQuote = "’"
			continue
		}
		switch line[idx] {
		case '`':
			if goCommentHasClosingQuote(line, idx, '`') {
				quote = '`'
			}
			continue
		case '"':
			if goCommentHasClosingQuote(line, idx, '"') {
				quote = '"'
			}
			continue
		case '\'':
			if end, ok := goCommentRuneLiteralEnd(line, idx); ok {
				idx = end
			}
			continue
		}
		switch line[idx] {
		case '?':
			if end, ok := goCommentSentenceEnd(line, idx); ok {
				return goCommentSentenceParts(line, end)
			}
		case '!':
			if goCommentIsSpacedUnaryNot(line, idx) {
				continue
			}
			if end, ok := goCommentSentenceEnd(line, idx); ok {
				return goCommentSentenceParts(line, end)
			}
		case '.':
			if goCommentIsDottedAbbreviation(line, idx) {
				continue
			}
			if goCommentIsDottedNumeric(line, idx) {
				continue
			}
			if end, ok := goCommentSentenceEnd(line, idx); ok {
				return goCommentSentenceParts(line, end)
			}
		}
	}
	return "", "", 0, 0, false
}

func goCommentIsDottedNumeric(line string, dotIndex int) bool {
	if dotIndex == 0 || dotIndex+1 >= len(line) ||
		line[dotIndex-1] < '0' || line[dotIndex-1] > '9' ||
		line[dotIndex+1] < '0' || line[dotIndex+1] > '9' {
		return false
	}
	start := dotIndex - 1
	for start >= 0 && (line[start] == '.' || line[start] >= '0' && line[start] <= '9') {
		start--
	}
	value := line[start+1 : dotIndex]
	return strings.ContainsRune(value, '.')
}

func goCommentSentenceEnd(line string, punctuationIndex int) (int, bool) {
	idx := punctuationIndex + 1
	for idx < len(line) {
		if strings.HasPrefix(line[idx:], "”") || strings.HasPrefix(line[idx:], "’") {
			idx += len("”")
			continue
		}
		runeValue, size := utf8.DecodeRuneInString(line[idx:])
		if strings.ContainsRune(")]}\"'", runeValue) || runeValue == '*' || runeValue == '_' || runeValue == '~' {
			idx += size
			continue
		}
		break
	}
	return idx, idx == len(line) || line[idx] == ' ' || line[idx] == '\t'
}

func goCommentIsSpacedUnaryNot(line string, operatorIndex int) bool {
	next := operatorIndex + 1
	for next < len(line) && (line[next] == ' ' || line[next] == '\t') {
		next++
	}
	if next == len(line) {
		return false
	}
	previous := operatorIndex - 1
	for previous >= 0 && (line[previous] == ' ' || line[previous] == '\t') {
		previous--
	}
	if previous < 0 {
		return true
	}
	if strings.ContainsRune("=([{,:;+-*/%&|^<>!", rune(line[previous])) {
		return true
	}
	prefix := strings.TrimSpace(line[:previous+1])
	for _, keyword := range []string{"if", "for", "return", "switch", "case"} {
		if strings.HasSuffix(prefix, keyword) &&
			(len(prefix) == len(keyword) || !goCommentIsIdentifierByte(prefix[len(prefix)-len(keyword)-1])) {
			return true
		}
	}
	return false
}

func goCommentHasClosingQuote(line string, start int, quote byte) bool {
	for idx := start + 1; idx < len(line); idx++ {
		if line[idx] == '\\' && quote != '`' {
			idx++
			continue
		}
		if line[idx] == quote {
			return true
		}
	}
	return false
}

func goCommentRuneLiteralEnd(line string, start int) (int, bool) {
	for idx := start + 1; idx < len(line); idx++ {
		if line[idx] == '\\' {
			idx++
			continue
		}
		if line[idx] != '\'' {
			continue
		}
		literal := line[start+1 : idx]
		if literal == "" || strings.ContainsAny(literal, " \t\r\n") {
			return 0, false
		}
		value, err := strconv.Unquote(line[start : idx+1])
		if err != nil || utf8.RuneCountInString(value) != 1 {
			return 0, false
		}
		return idx, true
	}
	return 0, false
}

func goCommentSentenceParts(line string, end int) (string, string, int, int, bool) {
	sentence := strings.TrimSpace(line[:end])
	rest := strings.TrimSpace(line[end:])
	sentenceOffset := len(line[:end]) - len(strings.TrimLeft(line[:end], " \t"))
	restOffset := end + len(line[end:]) - len(strings.TrimLeft(line[end:], " \t"))
	return sentence, rest, sentenceOffset, restOffset, true
}

func goCommentIsDottedAbbreviation(line string, dotIndex int) bool {
	if dotIndex > 0 && dotIndex+2 < len(line) && line[dotIndex+2] == '.' &&
		goCommentIsAbbreviationPair(line[dotIndex-1], line[dotIndex+1]) &&
		(dotIndex == 1 || line[dotIndex-2] != '.' && !goCommentIsIdentifierByte(line[dotIndex-2])) {
		return true
	}
	return dotIndex >= 3 && line[dotIndex-2] == '.' &&
		goCommentIsAbbreviationPair(line[dotIndex-3], line[dotIndex-1]) &&
		(dotIndex == 3 || line[dotIndex-4] != '.' && !goCommentIsIdentifierByte(line[dotIndex-4]))
}

func goCommentIsAbbreviationPair(first, second byte) bool {
	first = goCommentLowerASCII(first)
	second = goCommentLowerASCII(second)
	return first == 'e' && second == 'g' || first == 'i' && second == 'e'
}

func goCommentLowerASCII(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

func goCommentIsIdentifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func goCommentOpeningWord(sentence string) (string, int, bool, bool) {
	start := 0
	for start < len(sentence) && sentence[start] >= '0' && sentence[start] <= '9' {
		start++
	}
	orderedList := start > 0 && start < len(sentence) && sentence[start] == ')'
	if start > 0 && start < len(sentence) && sentence[start] == '.' && start+1 < len(sentence) && unicode.IsSpace(rune(sentence[start+1])) {
		orderedList = true
	}
	if !orderedList {
		start = 0
	} else {
		start++
	}
	for start < len(sentence) {
		if sentence[start] == '\\' && start+1 < len(sentence) && sentence[start+1] == '`' {
			start += 2
			continue
		}
		if span, found := goCommentLeadingLiteralSpan(sentence[start:]); found {
			start += span
			continue
		}
		if span, found := goCommentLeadingBacktickSpan(sentence[start:]); found {
			start += span
			continue
		}
		runeValue, size := utf8.DecodeRuneInString(sentence[start:])
		if unicode.IsSpace(runeValue) {
			start += size
			continue
		}
		if unicode.IsPunct(runeValue) || unicode.IsSymbol(runeValue) {
			start += size
			continue
		}
		break
	}
	if start >= len(sentence) {
		return "", 0, false, false
	}
	runeValue, _ := utf8.DecodeRuneInString(sentence[start:])
	if !unicode.IsLower(runeValue) {
		return "", 0, false, false
	}
	end := start
	for end < len(sentence) {
		runeValue, size := utf8.DecodeRuneInString(sentence[end:])
		if !unicode.IsLetter(runeValue) && !unicode.IsDigit(runeValue) && runeValue != '_' {
			break
		}
		end += size
	}
	exact := end == len(sentence)
	if !exact {
		runeValue, _ := utf8.DecodeRuneInString(sentence[end:])
		exact = goCommentIsDeclarationNameBoundary(runeValue)
	}
	return sentence[start:end], start, exact, true
}

func goCommentIsDeclarationNameBoundary(value rune) bool {
	if unicode.IsSpace(value) {
		return true
	}
	if unicode.Is(unicode.Hyphen, value) {
		return false
	}
	if strings.ContainsRune("/&|+=<>^~*!?%", value) {
		return false
	}
	return unicode.IsPunct(value) && !strings.ContainsRune("/", value)
}

func goCommentLeadingLiteralSpan(sentence string) (int, bool) {
	if sentence == "" {
		return 0, false
	}
	switch sentence[0] {
	case '`':
		return goCommentLeadingBacktickSpan(sentence)
	case '"':
		for idx := 1; idx < len(sentence); idx++ {
			if sentence[idx] == '\\' {
				idx++
				continue
			}
			if sentence[idx] == '"' {
				return idx + 1, true
			}
		}
	case '\'':
		end, found := goCommentRuneLiteralEnd(sentence, 0)
		if found {
			return end + 1, true
		}
	}
	return 0, false
}

func goCommentIsPunctuationOnlyOpening(sentence string) bool {
	if sentence == "" {
		return false
	}
	hasPunctuation := false
	for _, value := range sentence {
		if unicode.IsSpace(value) {
			continue
		}
		if !unicode.IsPunct(value) {
			return false
		}
		hasPunctuation = true
	}
	return hasPunctuation
}

func goCommentLeadingBacktickSpan(sentence string) (int, bool) {
	if sentence == "" || sentence[0] != '`' {
		return 0, false
	}
	runLength := backtickRunLength([]byte(sentence))
	closingOffset := matchingBacktickRun([]byte(sentence[runLength:]), runLength)
	if closingOffset < 0 {
		return 0, false
	}
	return runLength + closingOffset + runLength, true
}

func goCommentLooksLikeURL(sentence string) bool {
	prefix, found := goCommentURLPrefix(sentence)
	return found && goCommentIsURLPunctuation(strings.TrimSpace(sentence[prefix:]))
}

func goCommentURLPrefix(sentence string) (int, bool) {
	if sentence == "" {
		return 0, false
	}
	offset := 0
	closers := make([]string, 0, 2)
	for offset < len(sentence) {
		switch {
		case sentence[offset] == '(':
			closers = append(closers, ")")
			offset++
		case sentence[offset] == '<':
			closers = append(closers, ">")
			offset++
		case strings.HasPrefix(sentence[offset:], "“"):
			closers = append(closers, "”")
			offset += len("“")
		case strings.HasPrefix(sentence[offset:], "‘"):
			closers = append(closers, "’")
			offset += len("‘")
		default:
			goto url
		}
	}

url:
	start := offset
	parentheses := 0
	for offset < len(sentence) {
		runeValue, size := utf8.DecodeRuneInString(sentence[offset:])
		if unicode.IsSpace(runeValue) || strings.ContainsRune(">”’", runeValue) {
			break
		}
		if runeValue == '(' {
			parentheses++
		} else if runeValue == ')' {
			if parentheses == 0 {
				break
			}
			parentheses--
		}
		offset += size
	}
	if parentheses != 0 {
		return 0, false
	}
	if !goCommentIsURLToken(sentence[start:offset]) {
		return 0, false
	}
	for _, closer := range slices.Backward(closers) {
		if !strings.HasPrefix(sentence[offset:], closer) {
			return 0, false
		}
		offset += len(closer)
	}
	return offset, true
}

func goCommentIsURLPunctuation(text string) bool {
	for _, value := range text {
		if unicode.IsSpace(value) {
			continue
		}
		if !strings.ContainsRune(".,?!:;)]}>", value) {
			return false
		}
	}
	return true
}

func goCommentIsURLToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	trimmed := strings.TrimRight(value, ".?!:;")
	if _, err := netip.ParseAddr(trimmed); err == nil {
		return true
	}
	if _, err := netip.ParsePrefix(trimmed); err == nil {
		return true
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		if _, err := netip.ParseAddr(trimmed[1 : len(trimmed)-1]); err == nil {
			return true
		}
		if _, err := netip.ParsePrefix(trimmed[1 : len(trimmed)-1]); err == nil {
			return true
		}
	}
	if strings.HasPrefix(trimmed, "[") {
		if close := strings.IndexByte(trimmed, ']'); close > 1 {
			if address, err := netip.ParseAddr(trimmed[1:close]); err == nil && address.Is6() {
				return strings.ContainsAny(trimmed[close+1:], "/?#")
			}
		}
	}
	if strings.HasPrefix(trimmed, "/") && !strings.HasPrefix(trimmed, "//") ||
		strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, "../") {
		return goCommentLooksLikeRelativeURLPath(trimmed)
	}
	if goCommentLooksLikeHostnamePort(value) {
		return true
	}
	if goCommentLooksLikeHostnamePath(value) {
		return true
	}
	if strings.ContainsRune(trimmed, '.') && goCommentLooksLikeHostname(trimmed) {
		return true
	}
	if goCommentIsMACAddress(trimmed) {
		return true
	}
	if len(trimmed) > len("www.") && strings.EqualFold(trimmed[:len("www.")], "www.") {
		return true
	}
	if strings.Count(trimmed, "@") == 1 {
		parts := strings.SplitN(trimmed, "@", 2)
		if parts[0] != "" && goCommentLooksLikeHostname(parts[1]) {
			return true
		}
	}
	separator := strings.IndexByte(trimmed, ':')
	if separator <= 0 || !goCommentIsASCIIAlphanumericScheme(trimmed[:separator]) {
		return false
	}
	remainder := trimmed[separator+1:]
	if remainder == "" {
		return false
	}
	if strings.HasPrefix(remainder, "//") || strings.EqualFold(trimmed[:separator], "about") {
		return true
	}
	if goCommentIsOpaqueURI(trimmed[:separator], remainder) {
		return true
	}
	port := strings.TrimSuffix(remainder, ".")
	if strings.EqualFold(trimmed[:separator], "localhost") && port != "" && goCommentIsASCIIDigits(port) {
		return true
	}
	return strings.ContainsAny(remainder, "@/?#%=&+,;:")
}

func goCommentLooksLikeRelativeURLPath(value string) bool {
	if len(value) < 2 || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	start := 1
	if strings.HasPrefix(value, "./") {
		start = 2
	} else if strings.HasPrefix(value, "../") {
		start = 3
	}
	for _, runeValue := range value[start:] {
		if unicode.IsLetter(runeValue) || unicode.IsDigit(runeValue) {
			return true
		}
	}
	return false
}

func goCommentLooksLikeHostnamePort(sentence string) bool {
	value := strings.TrimRight(strings.TrimSpace(sentence), ".?!:;")
	if separator := strings.IndexAny(value, "/?#"); separator >= 0 {
		value = value[:separator]
	}
	separator := strings.LastIndexByte(value, ':')
	if separator <= 0 || separator+1 == len(value) {
		return false
	}
	host := value[:separator]
	port := value[separator+1:]
	if !goCommentIsASCIIDigits(port) {
		return false
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber > 65535 {
		return false
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
		address, err := netip.ParseAddr(host)
		return err == nil && address.Is6()
	}
	return goCommentLooksLikeHostname(host)
}

func goCommentLooksLikeHostnamePath(sentence string) bool {
	value := strings.TrimRight(strings.TrimSpace(sentence), ".?!:;")
	separator := strings.IndexAny(value, "/?#")
	if separator < 0 || separator == 0 {
		return separator == 0 && goCommentLooksLikeRelativeURLPath(value)
	}
	if separator+1 == len(value) && value[separator] != '/' {
		return false
	}
	host := value[:separator]
	if strings.ContainsRune(host, ':') {
		return goCommentLooksLikeHostnamePort(host)
	}
	if !strings.EqualFold(host, "localhost") && !strings.ContainsRune(host, '.') {
		return false
	}
	return goCommentLooksLikeHostname(host)
}

func goCommentLooksLikeHostname(host string) bool {
	if host == "" || host == "." {
		return false
	}
	for labelStart := 0; labelStart < len(host); {
		labelEnd := strings.IndexByte(host[labelStart:], '.')
		if labelEnd < 0 {
			labelEnd = len(host)
		} else {
			labelEnd += labelStart
		}
		if labelEnd == labelStart || host[labelStart] == '-' || host[labelEnd-1] == '-' {
			return false
		}
		for idx := labelStart; idx < labelEnd; idx++ {
			value := host[idx]
			if !goCommentIsLowerOrUpperLetter(value) && !(value >= '0' && value <= '9') && value != '-' {
				return false
			}
		}
		if labelEnd == len(host) {
			break
		}
		labelStart = labelEnd + 1
	}
	return true
}

func goCommentIsASCIIDigits(value string) bool {
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func goCommentIsMACAddress(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 6 {
		return false
	}
	for _, part := range parts {
		if len(part) != 2 {
			return false
		}
		for _, digit := range part {
			if !('0' <= digit && digit <= '9' || 'a' <= digit && digit <= 'f' || 'A' <= digit && digit <= 'F') {
				return false
			}
		}
	}
	return true
}

func goCommentIsASCIIAlphanumericScheme(scheme string) bool {
	if !goCommentIsLowerOrUpperLetter(scheme[0]) {
		return false
	}
	for idx := 1; idx < len(scheme); idx++ {
		value := scheme[idx]
		if !goCommentIsLowerOrUpperLetter(value) && !(value >= '0' && value <= '9') && value != '+' && value != '-' && value != '.' {
			return false
		}
	}
	return true
}

var goCommentOpaqueURISchemes = map[string]struct{}{
	"about": {}, "data": {}, "geo": {}, "mailto": {}, "news": {}, "tel": {}, "urn": {},
}

func goCommentIsOpaqueURI(scheme, remainder string) bool {
	if _, known := goCommentOpaqueURISchemes[strings.ToLower(scheme)]; !known || remainder == "" {
		return false
	}
	for _, value := range remainder {
		if unicode.IsSpace(value) || unicode.IsControl(value) {
			return false
		}
	}
	return true
}

func goCommentIsLowerOrUpperLetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func goCommentLooksLikeCode(sentence string) bool {
	if goCommentIsSpacedSingleWordList(sentence) {
		return false
	}
	if goCommentStartsWithWholeBacktickCode(sentence) || goCommentStartsWithCStyleCode(sentence) ||
		goCommentIsUnicodeArrowDiagram(sentence) || goCommentHasStructuralCodeSequence(sentence) {
		return true
	}
	if goCommentStartsWithStructuredCodePrefix(sentence) {
		return true
	}
	if goCommentStartsWithCStyleCode(sentence) {
		return true
	}
	if goCommentLooksLikeFileDeclarationCode(sentence) {
		return true
	}
	if strings.HasPrefix(sentence, "#") || strings.HasPrefix(sentence, "|") {
		return true
	}
	if goCommentLooksLikeStructuredCode(sentence) {
		return true
	}
	if goCommentStartsWithMarkdownEmphasis(sentence) {
		return false
	}
	return goCommentContainsCodeOperator(sentence)
}

func goCommentStartsWithStructuredCodePrefix(sentence string) bool {
	expression := goCommentTrimSentenceTerminal(sentence)
	tokens := goCommentScanTokens(expression)
	if len(tokens) == 3 && tokens[0].Kind == token.IDENT && tokens[1].Kind == token.ASSIGN &&
		tokens[2].Kind == token.INT && tokens[0].End == tokens[1].Start &&
		tokens[1].End == tokens[2].Start && strings.ContainsRune(tokens[0].Literal, '_') {
		return true
	}
	if len(tokens) < 3 || tokens[0].Kind != token.IDENT || tokens[1].Kind != token.SUB ||
		tokens[2].Kind != token.INT || tokens[0].End != tokens[1].Start ||
		tokens[1].End != tokens[2].Start {
		return false
	}
	hasColon := false
	indexedAssignments := 0
	for idx := 3; idx < len(tokens); idx++ {
		if tokens[idx].Kind == token.COLON {
			hasColon = true
			continue
		}
		if idx+3 >= len(tokens) || tokens[idx].Kind != token.LBRACK ||
			tokens[idx+1].Kind != token.INT || tokens[idx+2].Kind != token.RBRACK ||
			tokens[idx+3].Kind != token.ASSIGN {
			continue
		}
		indexedAssignments++
		idx += 3
	}
	return hasColon && indexedAssignments >= 2
}

func goCommentIsSpacedSingleWordList(sentence string) bool {
	trimmed := strings.TrimSpace(goCommentTrimSentenceTerminal(sentence))
	if len(trimmed) < 3 || (trimmed[0] != '-' && trimmed[0] != '+') || trimmed[1] != ' ' {
		return false
	}
	word := strings.TrimSpace(trimmed[2:])
	if word == "" {
		return false
	}
	for _, value := range word {
		if !unicode.IsLetter(value) && !unicode.IsDigit(value) && value != '_' {
			return false
		}
	}
	return true
}

func goCommentStartsWithCStyleCode(sentence string) bool {
	return goCommentIsWholeArrowSpan(goCommentScanTokens(goCommentTrimSentenceTerminal(sentence)))
}

func goCommentIsUnicodeArrowDiagram(sentence string) bool {
	text := strings.TrimSpace(goCommentTrimSentenceTerminal(sentence))
	if text == "" {
		return false
	}
	parts := make([]string, 0, 2)
	start := 0
	foundArrow := false
	for idx, value := range text {
		if !goCommentIsArrowRune(value) {
			continue
		}
		part := strings.TrimSpace(text[start:idx])
		tokens := goCommentTrimInsertedSemicolon(goCommentScanTokens(part))
		if part == "" || !goCommentIsArrowOperand(tokens, 0, len(tokens)) {
			return false
		}
		parts = append(parts, part)
		start = idx + len(string(value))
		foundArrow = true
	}
	if !foundArrow {
		return false
	}
	last := strings.TrimSpace(text[start:])
	if last == "" {
		return false
	}
	parts = append(parts, last)
	return len(parts) >= 2 && slices.ContainsFunc(parts, func(part string) bool {
		tokens := goCommentTrimInsertedSemicolon(goCommentScanTokens(part))
		return len(tokens) == 0 || !goCommentIsArrowOperand(tokens, 0, len(tokens))
	}) == false
}

func goCommentIsArrowRune(value rune) bool {
	return value >= 0x2190 && value <= 0x21ff ||
		value >= 0x27a0 && value <= 0x27af ||
		value >= 0x27b1 && value <= 0x27be ||
		value >= 0x27f0 && value <= 0x27ff ||
		value == 0x2794 || value >= 0x2798 && value <= 0x279f ||
		value >= 0x2900 && value <= 0x292a ||
		value >= 0x292d && value <= 0x297b ||
		value >= 0x2b00 && value <= 0x2b11 ||
		value >= 0x2b30 && value <= 0x2b4c ||
		value >= 0x2b4d && value <= 0x2b4f ||
		value >= 0x2b5a && value <= 0x2b7d ||
		value >= 0x2b80 && value <= 0x2b8f ||
		value >= 0x2b94 && value <= 0x2b95 ||
		value >= 0x2b98 && value <= 0x2baf ||
		value >= 0x2bb0 && value <= 0x2bb9 ||
		value >= 0x2bec && value <= 0x2bef ||
		value >= 0x1f800 && value <= 0x1f89b ||
		value >= 0x1f8a0 && value <= 0x1f8ab ||
		value >= 0x1f8b0 && value <= 0x1f8b1
}

func goCommentTrimInsertedSemicolon(tokens []goCommentToken) []goCommentToken {
	if len(tokens) > 0 && tokens[len(tokens)-1].Kind == token.SEMICOLON && tokens[len(tokens)-1].Literal != ";" {
		return tokens[:len(tokens)-1]
	}
	return tokens
}

func goCommentIsWholeArrowSpan(tokens []goCommentToken) bool {
	tokens = goCommentTrimInsertedSemicolon(tokens)
	if len(tokens) == 0 || !goCommentArrowDelimitersBalanced(tokens, 0, len(tokens)) ||
		!goCommentHasCStyleArrow(tokens, 0, len(tokens)) {
		return false
	}
	_, hasArrow, ok := goCommentParseArrowSequence(tokens, 0, len(tokens))
	return ok && hasArrow
}

func goCommentHasCStyleArrow(tokens []goCommentToken, start, end int) bool {
	for idx := start; idx+1 < end; idx++ {
		if goCommentIsCStyleArrowAt(tokens, idx) {
			return true
		}
	}
	return false
}

func goCommentIsCStyleArrowAt(tokens []goCommentToken, idx int) bool {
	if idx+1 >= len(tokens) || tokens[idx].End != tokens[idx+1].Start ||
		tokens[idx+1].Kind != token.GTR {
		return false
	}
	return tokens[idx].Kind == token.SUB || tokens[idx].Kind == token.ASSIGN
}

func goCommentParseArrowSequence(tokens []goCommentToken, start, end int) (int, bool, bool) {
	idx, hasArrow, ok := goCommentParseArrowTerm(tokens, start, end)
	if !ok {
		return 0, false, false
	}
	for idx < end {
		if goCommentIsCStyleArrowAt(tokens, idx) {
			next, _, termOK := goCommentParseArrowTerm(tokens, idx+2, end)
			if !termOK {
				return 0, false, false
			}
			hasArrow = true
			idx = next
			continue
		}
		if goCommentIsArrowQuestion(tokens[idx]) {
			colon := goCommentArrowConditionalColon(tokens, idx+1, end)
			if colon < 0 {
				return 0, false, false
			}
			next, nestedArrow, termOK := goCommentParseArrowSequence(tokens, idx+1, colon)
			if !termOK || next != colon {
				return 0, false, false
			}
			hasArrow = hasArrow || nestedArrow
			idx, nestedArrow, termOK = goCommentParseArrowSequence(tokens, colon+1, end)
			if !termOK {
				return 0, false, false
			}
			hasArrow = hasArrow || nestedArrow
			continue
		}
		if !goCommentIsArrowSequenceOperator(tokens[idx].Kind) {
			return 0, false, false
		}
		var nestedArrow bool
		idx, nestedArrow, ok = goCommentParseArrowTerm(tokens, idx+1, end)
		if !ok {
			return 0, false, false
		}
		hasArrow = hasArrow || nestedArrow
	}
	return idx, hasArrow, true
}

func goCommentArrowConditionalColon(tokens []goCommentToken, start, end int) int {
	depth := 0
	questionDepth := 0
	for idx := start; idx < end; idx++ {
		switch tokens[idx].Kind {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		case token.ILLEGAL:
			if depth == 0 && tokens[idx].Literal == "?" {
				questionDepth++
			}
		case token.COLON:
			if depth == 0 {
				if questionDepth > 0 {
					questionDepth--
					continue
				}
				return idx
			}
		}
	}
	return -1
}

func goCommentParseArrowTerm(tokens []goCommentToken, start, end int) (int, bool, bool) {
	if typeEnd, ok := goCommentArrowTypeTermEnd(tokens, start, end); ok {
		return typeEnd, false, true
	}
	idx := start
	if idx < end && tokens[idx].Kind == token.IDENT && tokens[idx].Literal == "sizeof" {
		idx++
	}
	for idx < end && goCommentIsArrowPrefixToken(tokens[idx].Kind) {
		idx++
	}
	if idx >= end {
		return 0, false, false
	}
	var hasArrow bool
	switch tokens[idx].Kind {
	case token.LPAREN, token.LBRACK, token.LBRACE:
		closing := token.RPAREN
		if tokens[idx].Kind == token.LBRACK {
			closing = token.RBRACK
		} else if tokens[idx].Kind == token.LBRACE {
			closing = token.RBRACE
		}
		close := goCommentMatchingToken(tokens, idx, tokens[idx].Kind, closing, end)
		if close <= idx {
			return 0, false, false
		}
		var groupOK bool
		_, hasArrow, groupOK = goCommentArrowGroupContent(tokens, idx+1, close)
		if !groupOK {
			return 0, false, false
		}
		idx = close + 1
		if idx < end && goCommentIsOperand(tokens[idx].Kind) &&
			tokens[close].End == tokens[idx].Start {
			idx++
		}
	case token.LSS, token.SHL:
		contentStart, closeStart, closeEnd, ok := goCommentMatchingAngleRun(tokens, idx, end)
		if !ok {
			return 0, false, false
		}
		var groupArrow bool
		_, groupArrow, groupOK := goCommentArrowGroupContent(tokens, contentStart, closeStart)
		if !groupOK {
			return 0, false, false
		}
		hasArrow = groupArrow
		idx = closeEnd
	default:
		if !goCommentIsOperand(tokens[idx].Kind) {
			return 0, false, false
		}
		idx++
	}
	for idx < end {
		if goCommentIsArrowPrefixToken(tokens[idx].Kind) && tokens[idx].Kind != token.INC && tokens[idx].Kind != token.DEC {
			prefixStart := idx
			for idx < end && goCommentIsArrowPrefixToken(tokens[idx].Kind) && tokens[idx].Kind != token.INC && tokens[idx].Kind != token.DEC {
				idx++
			}
			if idx >= end || !goCommentIsOperand(tokens[idx].Kind) {
				idx = prefixStart
				break
			}
			idx++
			continue
		}
		switch tokens[idx].Kind {
		case token.PERIOD:
			if idx+1 >= end {
				return 0, false, false
			}
			if tokens[idx+1].Kind == token.IDENT {
				idx += 2
				break
			}
			if tokens[idx+1].Kind != token.LPAREN {
				return 0, false, false
			}
			close := goCommentMatchingToken(tokens, idx+1, token.LPAREN, token.RPAREN, end)
			if close <= idx+1 {
				return 0, false, false
			}
			if !goCommentIsGoTypeTokens(tokens, idx+2, close) {
				return 0, false, false
			}
			idx = close + 1
		case token.LBRACK, token.LBRACE, token.LPAREN:
			closing := token.RBRACK
			if tokens[idx].Kind == token.LBRACE {
				closing = token.RBRACE
			} else if tokens[idx].Kind == token.LPAREN {
				closing = token.RPAREN
			}
			close := goCommentMatchingToken(tokens, idx, tokens[idx].Kind, closing, end)
			if close <= idx {
				return 0, false, false
			}
			var suffixOK bool
			var suffixArrow bool
			if close == idx+1 && (tokens[idx].Kind == token.LPAREN || tokens[idx].Kind == token.LBRACE) {
				suffixOK = true
			} else if tokens[idx].Kind == token.LBRACK && goCommentHasTopLevelColon(tokens, idx+1, close) {
				suffixOK = goCommentIsArrowSlice(tokens, idx+1, close)
			} else if tokens[idx].Kind == token.LPAREN &&
				(idx == 0 || tokens[idx-1].End != tokens[idx].Start) {
				_, suffixArrow, suffixOK = goCommentStrictArrowGroupContent(tokens, idx+1, close)
			} else {
				_, suffixArrow, suffixOK = goCommentArrowGroupContent(tokens, idx+1, close)
			}
			if !suffixOK {
				return 0, false, false
			}
			hasArrow = hasArrow || suffixArrow
			idx = close + 1
		case token.INC, token.DEC:
			idx++
		default:
			return idx, hasArrow, true
		}
	}
	return idx, hasArrow, true
}

func goCommentArrowTypeTermEnd(tokens []goCommentToken, start, end int) (int, bool) {
	if start >= end {
		return 0, false
	}
	depth := 0
	for idx := start; idx < end; idx++ {
		switch tokens[idx].Kind {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		}
		if depth != 0 || idx == start {
			continue
		}
		if goCommentIsCStyleArrowAt(tokens, idx) || goCommentIsArrowQuestion(tokens[idx]) ||
			goCommentIsArrowSequenceOperator(tokens[idx].Kind) {
			if goCommentIsGoTypeTokens(tokens, start, idx) || goCommentIsGoExpressionTokens(tokens, start, idx) {
				return idx, true
			}
		}
	}
	if goCommentIsGoTypeTokens(tokens, start, end) || goCommentIsGoExpressionTokens(tokens, start, end) {
		return end, true
	}
	return 0, false
}

func goCommentIsGoTypeTokens(tokens []goCommentToken, start, end int) bool {
	if start >= end {
		return false
	}
	if end == start+1 && tokens[start].Kind == token.TYPE {
		return true
	}
	source := goCommentTokenSource(tokens, start, end)
	_, err := parser.ParseFile(token.NewFileSet(), "type.go", []byte("package p\nvar _ "+source+"\n"), 0)
	return err == nil
}

func goCommentIsGoExpressionTokens(tokens []goCommentToken, start, end int) bool {
	if start >= end {
		return false
	}
	source := goCommentTokenSource(tokens, start, end)
	_, err := parser.ParseExpr(source)
	return err == nil
}

func goCommentTokenSource(tokens []goCommentToken, start, end int) string {
	var source strings.Builder
	previousEnd := -1
	for idx := start; idx < end; idx++ {
		if previousEnd >= 0 && tokens[idx].Start > previousEnd {
			source.WriteByte(' ')
		}
		literal := tokens[idx].Literal
		if literal == "" {
			literal = tokens[idx].Kind.String()
		}
		source.WriteString(literal)
		previousEnd = tokens[idx].End
	}
	return source.String()
}

func goCommentArrowGroupContent(tokens []goCommentToken, start, end int) (int, bool, bool) {
	if start >= end || !goCommentArrowDelimitersBalanced(tokens, start, end) {
		return 0, false, false
	}
	if !goCommentHasCStyleArrow(tokens, start, end) {
		return end, false, goCommentIsArrowGroup(tokens, start, end)
	}
	return goCommentParseArrowSequence(tokens, start, end)
}

func goCommentStrictArrowGroupContent(tokens []goCommentToken, start, end int) (int, bool, bool) {
	if start >= end || !goCommentArrowDelimitersBalanced(tokens, start, end) {
		return 0, false, false
	}
	next, hasArrow, ok := goCommentParseArrowSequence(tokens, start, end)
	return next, hasArrow, ok && next == end
}

func goCommentIsArrowSlice(tokens []goCommentToken, start, end int) bool {
	if start > end || !goCommentArrowDelimitersBalanced(tokens, start, end) {
		return false
	}
	colonCount := 0
	segmentStart := start
	segments := make([][2]int, 0, 3)
	depth := 0
	for idx := start; idx < end; idx++ {
		switch tokens[idx].Kind {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		case token.COLON:
			if depth != 0 {
				continue
			}
			colonCount++
			if colonCount > 2 {
				return false
			}
			segments = append(segments, [2]int{segmentStart, idx})
			segmentStart = idx + 1
		}
	}
	if colonCount == 0 {
		return false
	}
	segments = append(segments, [2]int{segmentStart, end})
	for _, segment := range segments {
		if segment[0] == segment[1] {
			if colonCount == 2 {
				return false
			}
			continue
		}
		if _, _, ok := goCommentStrictArrowGroupContent(tokens, segment[0], segment[1]); !ok {
			return false
		}
	}
	return true
}

func goCommentHasTopLevelColon(tokens []goCommentToken, start, end int) bool {
	depth := 0
	for idx := start; idx < end; idx++ {
		switch tokens[idx].Kind {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		case token.COLON:
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

func goCommentIsArrowQuestion(current goCommentToken) bool {
	return current.Kind == token.ILLEGAL && current.Literal == "?"
}

func goCommentIsArrowSequenceOperator(kind token.Token) bool {
	switch kind {
	case token.ADD, token.SUB, token.MUL, token.QUO, token.REM,
		token.AND, token.OR, token.XOR, token.SHL, token.SHR, token.AND_NOT,
		token.LAND, token.LOR, token.EQL, token.NEQ, token.LSS, token.LEQ,
		token.GTR, token.GEQ, token.ASSIGN, token.DEFINE, token.COMMA,
		token.SEMICOLON, token.COLON, token.ADD_ASSIGN, token.SUB_ASSIGN,
		token.MUL_ASSIGN, token.QUO_ASSIGN, token.REM_ASSIGN, token.AND_ASSIGN,
		token.OR_ASSIGN, token.XOR_ASSIGN, token.SHL_ASSIGN, token.SHR_ASSIGN,
		token.AND_NOT_ASSIGN, token.ARROW, token.ELLIPSIS:
		return true
	default:
		return false
	}
}

func goCommentMatchingAngleRun(tokens []goCommentToken, start, end int) (int, int, int, bool) {
	openingWidth := 0
	contentStart := start
	for contentStart < end {
		width := goCommentOpeningAngleRunWidth(tokens[contentStart].Kind)
		if width == 0 || contentStart > start && tokens[contentStart-1].End != tokens[contentStart].Start {
			break
		}
		openingWidth += width
		contentStart++
	}
	if openingWidth == 0 {
		return 0, 0, 0, false
	}
	closingStart, closingEnd := -1, -1
	for idx := contentStart; idx < end; {
		if goCommentClosingAngleRunWidth(tokens[idx].Kind) == 0 {
			idx++
			continue
		}
		runStart := idx
		runWidth := 0
		runValid := true
		for idx < end {
			width := goCommentClosingAngleRunWidth(tokens[idx].Kind)
			if width == 0 || idx > runStart && tokens[idx-1].End != tokens[idx].Start {
				break
			}
			if tokens[idx].Kind == token.GTR && idx > start && goCommentIsCStyleArrowAt(tokens, idx-1) {
				runValid = false
			}
			runWidth += width
			idx++
		}
		if !runValid || runWidth != openingWidth ||
			!goCommentArrowDelimitersBalanced(tokens, contentStart, runStart) {
			continue
		}
		if idx < end && tokens[idx].Kind == token.IDENT &&
			tokens[idx-1].End != tokens[idx].Start {
			continue
		}
		if _, _, ok := goCommentArrowGroupContent(tokens, contentStart, runStart); !ok {
			continue
		}
		closingStart, closingEnd = runStart, idx
	}
	return contentStart, closingStart, closingEnd, closingStart >= 0
}

func goCommentOpeningAngleRunWidth(kind token.Token) int {
	switch kind {
	case token.LSS:
		return 1
	case token.SHL:
		return 2
	default:
		return 0
	}
}

func goCommentClosingAngleRunWidth(kind token.Token) int {
	switch kind {
	case token.GTR:
		return 1
	case token.SHR:
		return 2
	default:
		return 0
	}
}

func goCommentIsArrowOperand(tokens []goCommentToken, start, end int) bool {
	if start >= end || !goCommentArrowDelimitersBalanced(tokens, start, end) {
		return false
	}
	operandEnd, ok := goCommentArrowOperandEnd(tokens, start, end)
	return ok && operandEnd == end
}

func goCommentArrowOperandEnd(tokens []goCommentToken, start, end int) (int, bool) {
	idx := start
	if idx < end && tokens[idx].Kind == token.IDENT && tokens[idx].Literal == "sizeof" {
		idx++
	}
	for idx < end && goCommentIsArrowPrefixToken(tokens[idx].Kind) {
		idx++
	}
	if idx >= end {
		return 0, false
	}
	switch tokens[idx].Kind {
	case token.LPAREN:
		close := goCommentMatchingToken(tokens, idx, token.LPAREN, token.RPAREN, end)
		if close <= idx || !goCommentIsArrowGroup(tokens, idx+1, close) {
			return 0, false
		}
		idx = close + 1
		if idx < end && goCommentIsOperand(tokens[idx].Kind) &&
			tokens[close].End == tokens[idx].Start {
			idx++
		}
	case token.LBRACE:
		close := goCommentMatchingToken(tokens, idx, token.LBRACE, token.RBRACE, end)
		if close <= idx || !goCommentIsArrowGroup(tokens, idx+1, close) {
			return 0, false
		}
		idx = close + 1
	default:
		if !goCommentIsOperand(tokens[idx].Kind) {
			return 0, false
		}
		idx++
	}
	for idx < end {
		switch tokens[idx].Kind {
		case token.PERIOD:
			if idx+1 >= end || tokens[idx+1].Kind != token.IDENT {
				return 0, false
			}
			idx += 2
		case token.LBRACK, token.LBRACE:
			close := token.RBRACK
			if tokens[idx].Kind == token.LBRACE {
				close = token.RBRACE
			}
			match := goCommentMatchingToken(tokens, idx, tokens[idx].Kind, close, end)
			if match <= idx || !goCommentIsArrowGroup(tokens, idx+1, match) {
				return 0, false
			}
			idx = match + 1
		case token.LPAREN:
			if idx == 0 || tokens[idx-1].End != tokens[idx].Start {
				return 0, false
			}
			match := goCommentMatchingToken(tokens, idx, token.LPAREN, token.RPAREN, end)
			if match <= idx || !goCommentIsArrowGroup(tokens, idx+1, match) {
				return 0, false
			}
			idx = match + 1
		case token.INC, token.DEC:
			idx++
		default:
			return 0, false
		}
	}
	return idx, true
}

func goCommentIsArrowGroup(tokens []goCommentToken, start, end int) bool {
	if start >= end || !goCommentArrowDelimitersBalanced(tokens, start, end) {
		return false
	}
	if goCommentIsArrowGroupOperator(tokens[end-1].Kind) ||
		tokens[start].Kind == token.COMMA || tokens[end-1].Kind == token.COMMA {
		return false
	}
	for idx := start; idx < end; idx++ {
		if tokens[idx].Kind != token.ILLEGAL || tokens[idx].Literal != "?" {
			continue
		}
		if idx+1 >= end || tokens[idx+1].Kind == token.COLON {
			return false
		}
		foundColon := false
		for next := idx + 1; next < end; next++ {
			if tokens[next].Kind == token.COLON {
				foundColon = true
				break
			}
		}
		if !foundColon {
			return false
		}
	}
	return true
}

func goCommentIsArrowGroupOperator(kind token.Token) bool {
	switch kind {
	case token.ADD, token.SUB, token.QUO, token.REM,
		token.AND, token.OR, token.XOR, token.SHL, token.SHR, token.AND_NOT,
		token.LAND, token.LOR, token.EQL, token.NEQ, token.LSS, token.LEQ,
		token.GTR, token.GEQ, token.ASSIGN, token.DEFINE, token.NOT, token.TILDE,
		token.COLON, token.SEMICOLON:
		return true
	default:
		return false
	}
}

func goCommentIsArrowPrefixToken(kind token.Token) bool {
	switch kind {
	case token.MUL, token.AND, token.ADD, token.SUB, token.NOT, token.TILDE,
		token.INC, token.DEC, token.ARROW:
		return true
	default:
		return false
	}
}

func goCommentArrowDelimitersBalanced(tokens []goCommentToken, start, end int) bool {
	stack := make([]token.Token, 0, 2)
	for idx := start; idx < end; idx++ {
		switch tokens[idx].Kind {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			stack = append(stack, tokens[idx].Kind)
		case token.RPAREN, token.RBRACK, token.RBRACE:
			if len(stack) == 0 || !goCommentClosingDelimiterMatches(stack[len(stack)-1], tokens[idx].Kind) {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) == 0
}

func goCommentClosingDelimiterMatches(opening, closing token.Token) bool {
	return opening == token.LPAREN && closing == token.RPAREN ||
		opening == token.LBRACK && closing == token.RBRACK ||
		opening == token.LBRACE && closing == token.RBRACE
}

func goCommentMatchingToken(tokens []goCommentToken, start int, opening, closing token.Token, end int) int {
	depth := 0
	for idx := start; idx < end; idx++ {
		switch tokens[idx].Kind {
		case opening:
			depth++
		case closing:
			depth--
			if depth == 0 {
				return idx
			}
		}
	}
	return -1
}

func goCommentLooksLikeWholeLineCode(sentence string) bool {
	if goCommentStartsWithWholeBacktickCode(sentence) || goCommentStartsWithCStyleCode(sentence) ||
		goCommentIsUnicodeArrowDiagram(sentence) || strings.HasPrefix(sentence, "#") || strings.HasPrefix(sentence, "|") {
		return true
	}
	expression := goCommentTrimSentenceTerminal(sentence)
	if goCommentLooksLikeFileDeclarationCode(expression) {
		return true
	}
	if goCommentLooksLikeElseStatement(expression) {
		return true
	}
	if parsed, err := parser.ParseExpr(expression); err == nil {
		tokens := goCommentScanTokens(expression)
		if goCommentIsCodeExpression(parsed, tokens) || goCommentHasTightStructuredDelimiter(tokens) {
			return true
		}
	}
	if goCommentLooksLikeMemberCode(expression) {
		return true
	}
	source := "package p\nfunc snippet() {\n" + expression + "\n}\n"
	file, err := parser.ParseFile(token.NewFileSet(), "snippet.go", source, 0)
	if err != nil {
		trimmed := strings.TrimSpace(expression)
		if goCommentStartsSwitchClause(trimmed) {
			source = "package p\nfunc snippet() {\nswitch {\n" + expression + "\n}\n}\n"
			file, err = parser.ParseFile(token.NewFileSet(), "snippet.go", source, 0)
		}
		if err != nil && goCommentLooksLikeSelectClause(trimmed) {
			return true
		}
	}
	if err != nil {
		return false
	}
	funcDecl := file.Decls[0].(*ast.FuncDecl)
	if len(funcDecl.Body.List) != 1 {
		return goCommentAllStatementsCode(funcDecl.Body.List) || goCommentHasExplicitStatementBlock(funcDecl.Body.List)
	}
	statement := funcDecl.Body.List[0]
	if _, ok := statement.(*ast.ExprStmt); ok {
		return goCommentHasTightStructuredDelimiter(goCommentScanTokens(expression))
	}
	return goCommentIsCodeStatement(statement) || goCommentHasExplicitStatementBlock(funcDecl.Body.List)
}

func goCommentLooksLikeFileDeclarationCode(expression string) bool {
	expression = goCommentTrimSentenceTerminal(expression)
	if expression == "" {
		return false
	}
	source := "package fixture\n" + expression + "\n"
	packageDeclaration := strings.HasPrefix(expression, "package ") &&
		(len(expression) == len("package ") || goCommentIsIdentifierByte(expression[len("package ")]))
	if packageDeclaration {
		source = expression + "\n"
	}
	file, err := parser.ParseFile(token.NewFileSet(), "snippet.go", source, 0)
	if err != nil {
		return false
	}
	if packageDeclaration {
		return file.Name != nil
	}
	return file.Name != nil && len(file.Decls) > 0
}

func goCommentHasStructuralCodeSequence(sentence string) bool {
	tokens := goCommentScanTokens(goCommentTrimSentenceTerminal(sentence))
	for idx, current := range tokens {
		if idx > 1 {
			continue
		}
		if current.Kind == token.DEFINE && goCommentHasOperandSides(tokens, idx) && goCommentIsWholeOperatorSpan(tokens, idx) {
			return true
		}
		if idx+1 == len(tokens) || current.End != tokens[idx+1].Start {
			continue
		}
		if (current.Kind == token.SUB || current.Kind == token.ASSIGN) && tokens[idx+1].Kind == token.GTR &&
			goCommentIsWholeArrowSpan(tokens) {
			return true
		}
	}
	return false
}

func goCommentStartsWithWholeBacktickCode(sentence string) bool {
	span, found := goCommentLeadingBacktickSpan(sentence)
	if !found {
		return false
	}
	suffix := strings.TrimSpace(sentence[span:])
	return goCommentTrimSentenceTerminal(suffix) == ""
}

func goCommentStartsWithMarkdownEmphasis(sentence string) bool {
	for _, marker := range []string{"**", "__", "~~", "*", "_"} {
		if !strings.HasPrefix(sentence, marker) {
			continue
		}
		end := strings.Index(sentence[len(marker):], marker)
		if end >= 1 {
			return true
		}
	}
	return false
}

func goCommentLooksLikeStructuredCode(sentence string) bool {
	expression := goCommentTrimSentenceTerminal(sentence)
	if goCommentStartsWithCStyleCode(sentence) {
		return true
	}
	if parsed, err := parser.ParseExpr(expression); err == nil {
		tokens := goCommentScanTokens(expression)
		if goCommentIsCodeExpression(parsed, tokens) || goCommentHasTightStructuredDelimiter(tokens) {
			return true
		}
	}
	if goCommentLooksLikeMemberCode(expression) {
		return true
	}
	return goCommentLooksLikeStatementCode(expression)
}

func goCommentLooksLikeMemberCode(expression string) bool {
	for _, typeKeyword := range []string{"struct", "interface"} {
		source := "package p\ntype snippet " + typeKeyword + " {\n" + expression + "\n}\n"
		file, err := parser.ParseFile(token.NewFileSet(), "snippet.go", source, 0)
		if err != nil || len(file.Decls) != 1 {
			continue
		}
		decl, ok := file.Decls[0].(*ast.GenDecl)
		if !ok || len(decl.Specs) != 1 {
			continue
		}
		typeSpec, ok := decl.Specs[0].(*ast.TypeSpec)
		if !ok {
			continue
		}
		var fields *ast.FieldList
		switch typeNode := typeSpec.Type.(type) {
		case *ast.StructType:
			fields = typeNode.Fields
		case *ast.InterfaceType:
			fields = typeNode.Methods
		}
		if fields != nil && len(fields.List) == 1 && goCommentIsMemberField(fields.List[0]) {
			return true
		}
	}
	return false
}

func goCommentIsMemberField(field *ast.Field) bool {
	if field == nil || len(field.Names) == 0 {
		return false
	}
	if functionType, ok := field.Type.(*ast.FuncType); ok {
		if functionType.Func != token.NoPos {
			return true
		}
		if functionType.Params.Opening != field.Names[0].End() {
			return false
		}
		for _, parameter := range functionType.Params.List {
			if parameter.Type == nil {
				return false
			}
		}
		return true
	}
	return goCommentIsUnambiguousMemberType(field.Type)
}

func goCommentIsUnambiguousMemberType(typeNode ast.Expr) bool {
	switch typeNode := typeNode.(type) {
	case *ast.Ident:
		return goCommentLooksLikeTypeName(typeNode.Name)
	case *ast.ArrayType:
		return typeNode.Elt != nil
	case *ast.ChanType, *ast.FuncType, *ast.InterfaceType,
		*ast.MapType, *ast.SelectorExpr, *ast.StarExpr, *ast.StructType:
		return true
	case *ast.IndexExpr:
		return true
	case *ast.IndexListExpr:
		return true
	case *ast.ParenExpr:
		if ident, ok := typeNode.X.(*ast.Ident); ok {
			return goCommentLooksLikeCamelCaseType(ident.Name)
		}
		return goCommentIsUnambiguousMemberType(typeNode.X)
	default:
		return false
	}
}

func goCommentLooksLikeCamelCaseType(name string) bool {
	first, _ := utf8.DecodeRuneInString(name)
	if unicode.IsUpper(first) {
		return true
	}
	for _, value := range name {
		if unicode.IsUpper(value) {
			return true
		}
	}
	return false
}

func goCommentLooksLikeTypeName(name string) bool {
	if slices.Contains([]string{"any", "bool", "byte", "complex64", "complex128", "error", "float32", "float64", "int", "int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr"}, name) {
		return true
	}
	first, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(first)
}

func goCommentTrimSentenceTerminal(sentence string) string {
	sentence = strings.TrimSpace(sentence)
	if sentence != "" && strings.ContainsRune(".?!", rune(sentence[len(sentence)-1])) {
		return strings.TrimSpace(sentence[:len(sentence)-1])
	}
	return sentence
}

func goCommentIsCodeExpression(expression ast.Expr, tokens []goCommentToken) bool {
	switch expression.(type) {
	case *ast.Ident:
		name := expression.(*ast.Ident).Name
		return name == "true" || name == "false" || name == "nil"
	case *ast.BinaryExpr:
		return !goCommentHasAmbiguousTightOperator(tokens)
	case *ast.ArrayType, *ast.BasicLit, *ast.ChanType, *ast.Ellipsis,
		*ast.FuncType, *ast.InterfaceType, *ast.MapType, *ast.ParenExpr,
		*ast.SelectorExpr, *ast.StarExpr, *ast.StructType, *ast.UnaryExpr:
		return true
	case *ast.CallExpr:
		return goCommentIsRecognizedCall(expression.(*ast.CallExpr), tokens)
	default:
		return false
	}
}

func goCommentIsRecognizedCall(call *ast.CallExpr, tokens []goCommentToken) bool {
	if len(call.Args) == 0 || call.Ellipsis.IsValid() || goCommentHasCallTrailingComma(tokens) {
		return true
	}
	callee, bare := call.Fun.(*ast.Ident)
	if !bare {
		return true
	}
	if goCommentLooksLikeTypeName(callee.Name) {
		return true
	}
	if len(call.Args) != 1 {
		return true
	}
	argument, plain := call.Args[0].(*ast.Ident)
	if !plain {
		return true
	}
	return argument.Name == "true" || argument.Name == "false" || argument.Name == "nil" ||
		argument.Name == "_" || argument.Name == "iota"
}

func goCommentHasCallTrailingComma(tokens []goCommentToken) bool {
	stack := make([]token.Token, 0, 2)
	for idx, current := range tokens {
		switch current.Kind {
		case token.LPAREN:
			stack = append(stack, current.Kind)
		case token.RPAREN:
			if len(stack) == 0 {
				continue
			}
			if idx > 0 && tokens[idx-1].Kind == token.COMMA {
				return true
			}
			stack = stack[:len(stack)-1]
		}
	}
	return false
}

func goCommentLooksLikeStatementCode(expression string) bool {
	if goCommentLooksLikeElseStatement(expression) {
		return true
	}
	source := "package p\nfunc snippet() {\n" + expression + "\n}\n"
	file, err := parser.ParseFile(token.NewFileSet(), "snippet.go", source, 0)
	if err != nil {
		trimmed := strings.TrimSpace(expression)
		if goCommentStartsSwitchClause(trimmed) {
			source = "package p\nfunc snippet() {\nswitch {\n" + expression + "\n}\n}\n"
			file, err = parser.ParseFile(token.NewFileSet(), "snippet.go", source, 0)
		}
		if err != nil && goCommentLooksLikeSelectClause(trimmed) {
			return true
		}
		if err != nil {
			return false
		}
	}
	funcDecl := file.Decls[0].(*ast.FuncDecl)
	tokens := goCommentScanTokens(expression)
	if goCommentHasExplicitTargetBranch(funcDecl.Body.List, tokens) {
		return true
	}
	if len(funcDecl.Body.List) != 1 {
		return goCommentAllStatementsCode(funcDecl.Body.List) || goCommentHasExplicitStatementBlock(funcDecl.Body.List)
	}
	statement := funcDecl.Body.List[0]
	if _, ok := statement.(*ast.ExprStmt); !ok {
		return goCommentIsCodeStatement(statement) || goCommentContainsCodeOperator(expression) || goCommentHasExplicitStatementBlock(funcDecl.Body.List)
	}
	return goCommentHasTightStructuredDelimiter(tokens)
}

func goCommentAllStatementsCode(statements []ast.Stmt) bool {
	return len(statements) > 0 && !slices.ContainsFunc(statements, func(statement ast.Stmt) bool {
		return !goCommentIsCodeStatement(statement)
	})
}

func goCommentIsCodeStatement(statement ast.Stmt) bool {
	if goCommentIsUnambiguousStatement(statement) {
		return true
	}
	expression, ok := statement.(*ast.ExprStmt)
	if !ok {
		return false
	}
	switch expression := expression.X.(type) {
	case *ast.BasicLit, *ast.BinaryExpr, *ast.CallExpr, *ast.CompositeLit,
		*ast.FuncLit, *ast.IndexExpr, *ast.IndexListExpr, *ast.SelectorExpr,
		*ast.SliceExpr, *ast.StarExpr, *ast.TypeAssertExpr, *ast.UnaryExpr:
		return true
	case *ast.Ident:
		return slices.Contains([]string{"false", "nil", "true"}, expression.Name)
	case *ast.ParenExpr:
		return goCommentIsCodeExpressionNode(expression.X)
	default:
		return false
	}
}

func goCommentIsCodeExpressionNode(expression ast.Expr) bool {
	switch expression := expression.(type) {
	case *ast.BasicLit, *ast.BinaryExpr, *ast.CallExpr, *ast.CompositeLit,
		*ast.FuncLit, *ast.IndexExpr, *ast.IndexListExpr, *ast.SelectorExpr,
		*ast.SliceExpr, *ast.StarExpr, *ast.TypeAssertExpr, *ast.UnaryExpr:
		return true
	case *ast.Ident:
		return slices.Contains([]string{"false", "nil", "true"}, expression.Name)
	case *ast.ParenExpr:
		return goCommentIsCodeExpressionNode(expression.X)
	default:
		return false
	}
}

func goCommentHasExplicitTargetBranch(statements []ast.Stmt, tokens []goCommentToken) bool {
	hasSemicolon := slices.ContainsFunc(tokens, func(current goCommentToken) bool {
		return current.Kind == token.SEMICOLON && current.Literal == ";"
	})
	if !hasSemicolon {
		return false
	}
	return slices.ContainsFunc(statements, func(statement ast.Stmt) bool {
		branch, ok := statement.(*ast.BranchStmt)
		return ok && (branch.Tok == token.BREAK || branch.Tok == token.CONTINUE) && branch.Label != nil
	})
}

func goCommentLooksLikeElseStatement(expression string) bool {
	expression = strings.TrimSpace(expression)
	if !strings.HasPrefix(expression, "else") ||
		(len(expression) > len("else") && goCommentIsIdentifierByte(expression[len("else")])) {
		return false
	}
	source := "package p\nfunc snippet() {\nif true {} " + expression + "\n}\n"
	file, err := parser.ParseFile(token.NewFileSet(), "snippet.go", source, 0)
	if err != nil {
		return false
	}
	funcDecl := file.Decls[0].(*ast.FuncDecl)
	return len(funcDecl.Body.List) == 1 && goCommentHasExplicitStatementBlock(funcDecl.Body.List)
}

func goCommentStartsSwitchClause(expression string) bool {
	for _, keyword := range []string{"case", "default"} {
		if !strings.HasPrefix(expression, keyword) {
			continue
		}
		rest := expression[len(keyword):]
		if rest == "" || goCommentIsIdentifierByte(rest[0]) {
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		if keyword == "default" && strings.HasPrefix(rest, ":") {
			return true
		}
		if keyword == "case" && strings.ContainsRune(rest, ':') {
			return true
		}
	}
	return false
}

func goCommentLooksLikeSelectClause(expression string) bool {
	expression = strings.TrimSpace(expression)
	if !strings.HasPrefix(expression, "case") ||
		(len(expression) > len("case") && goCommentIsIdentifierByte(expression[len("case")])) {
		return false
	}
	rest := strings.TrimSpace(expression[len("case"):])
	if rest == "" {
		return false
	}
	parseSelect := func(candidate string) (*ast.SelectStmt, bool) {
		source := "package p\nfunc snippet() {\nselect {\n" + candidate + "\n}\n}\n"
		file, err := parser.ParseFile(token.NewFileSet(), "snippet.go", source, 0)
		if err != nil {
			return nil, false
		}
		funcDecl := file.Decls[0].(*ast.FuncDecl)
		selectStmt, ok := funcDecl.Body.List[0].(*ast.SelectStmt)
		if !ok || len(selectStmt.Body.List) != 1 {
			return nil, false
		}
		return selectStmt, true
	}
	selectStmt, ok := parseSelect(expression)
	if !ok {
		selectStmt, ok = parseSelect(expression + ":")
	}
	if !ok {
		return false
	}
	clause, ok := selectStmt.Body.List[0].(*ast.CommClause)
	if !ok || clause.Comm == nil || !goCommentIsUnambiguousStatement(clause.Comm) {
		if !ok || clause.Comm == nil {
			return false
		}
		expression, expressionOK := clause.Comm.(*ast.ExprStmt)
		if !expressionOK || !goCommentIsCodeStatement(expression) {
			return false
		}
	}
	return len(clause.Body) == 0 || goCommentAllStatementsCode(clause.Body)
}

func goCommentIsUnambiguousStatement(statement ast.Stmt) bool {
	switch statement := statement.(type) {
	case *ast.AssignStmt, *ast.DeclStmt, *ast.DeferStmt, *ast.GoStmt, *ast.IncDecStmt,
		*ast.ReturnStmt, *ast.SendStmt:
		return true
	case *ast.BranchStmt:
		switch statement.Tok {
		case token.GOTO, token.FALLTHROUGH:
			return true
		case token.BREAK, token.CONTINUE:
			return statement.Label == nil
		}
	case *ast.LabeledStmt:
		if branch, ok := statement.Stmt.(*ast.BranchStmt); ok {
			if branch.Tok == token.BREAK || branch.Tok == token.CONTINUE {
				return branch.Label != nil
			}
			if branch.Tok == token.GOTO {
				return true
			}
		}
		if _, ok := statement.Stmt.(*ast.ReturnStmt); ok {
			return true
		}
		if goCommentIsCodeStatement(statement.Stmt) {
			return true
		}
		return goCommentStatementHasExplicitBlock(statement.Stmt)
	}
	return false
}

func goCommentHasTightStructuredDelimiter(tokens []goCommentToken) bool {
	for idx, current := range tokens {
		if (current.Kind == token.LPAREN || current.Kind == token.LBRACK || current.Kind == token.LBRACE) &&
			idx > 0 && tokens[idx-1].End == current.Start {
			return true
		}
	}
	return false
}

func goCommentHasExplicitStatementBlock(statements []ast.Stmt) bool {
	return slices.ContainsFunc(statements, goCommentStatementHasExplicitBlock)
}

func goCommentStatementHasExplicitBlock(statement ast.Stmt) bool {
	switch statement := statement.(type) {
	case *ast.LabeledStmt:
		return goCommentStatementHasExplicitBlock(statement.Stmt)
	case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt,
		*ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		return true
	default:
		return false
	}
}

func goCommentContainsCodeOperator(sentence string) bool {
	sentence = goCommentTrimSentenceTerminal(sentence)
	tokens := goCommentScanTokens(sentence)
	for idx, current := range tokens {
		if !current.Kind.IsOperator() || goCommentIsDelimiter(current.Kind) {
			continue
		}
		if idx > 1 {
			continue
		}
		if !goCommentIsWholeOperatorSpan(tokens, idx) {
			continue
		}
		if current.Kind == token.ARROW {
			return true
		}
		if (current.Kind == token.INC || current.Kind == token.DEC) && goCommentHasPostfixOperand(tokens, idx) {
			return true
		}
		if current.Kind != token.INC && current.Kind != token.DEC && goCommentHasOperandSides(tokens, idx) {
			return true
		}
		if current.Kind != token.INC && current.Kind != token.DEC && goCommentHasUnaryRHSOperand(tokens, idx) {
			return true
		}
	}
	return false
}

func goCommentIsWholeOperatorSpan(tokens []goCommentToken, operatorIndex int) bool {
	end := len(tokens)
	if end > 0 && tokens[end-1].Kind == token.SEMICOLON {
		end--
	}
	if operatorIndex != 1 || operatorIndex >= end {
		return false
	}
	operator := tokens[operatorIndex].Kind
	if operator == token.INC || operator == token.DEC {
		return end == 2 && goCommentIsOperand(tokens[0].Kind)
	}
	if operator == token.ARROW {
		return end == 3 && goCommentIsOperand(tokens[0].Kind) && goCommentIsOperand(tokens[2].Kind)
	}
	return false
}

func goCommentScanTokens(source string) []goCommentToken {
	sourceBytes := goCommentMaskSmartQuotes([]byte(source))
	fileSet := token.NewFileSet()
	file := fileSet.AddFile("comment", -1, len(sourceBytes))
	var sourceScanner scanner.Scanner
	sourceScanner.Init(file, sourceBytes, nil, 0)

	tokens := make([]goCommentToken, 0)
	for {
		position, kind, literal := sourceScanner.Scan()
		if kind == token.EOF {
			break
		}
		start := file.Offset(position)
		length := len(literal)
		if length == 0 {
			length = len(kind.String())
		}
		tokens = append(tokens, goCommentToken{
			Kind:    kind,
			Literal: literal,
			Start:   start,
			End:     start + length,
		})
	}
	return tokens
}

func goCommentMaskSmartQuotes(source []byte) []byte {
	masked := slices.Clone(source)
	for idx := 0; idx < len(masked); {
		openLength, close := 0, ""
		if bytes.HasPrefix(masked[idx:], []byte("“")) {
			openLength, close = len("“"), "”"
		} else if bytes.HasPrefix(masked[idx:], []byte("‘")) {
			openLength, close = len("‘"), "’"
		}
		if openLength == 0 {
			idx++
			continue
		}
		closeOffset := bytes.Index(masked[idx+openLength:], []byte(close))
		if closeOffset < 0 {
			idx += openLength
			continue
		}
		end := idx + openLength + closeOffset + len(close)
		for maskIndex := idx; maskIndex < end; maskIndex++ {
			if masked[maskIndex] != '\n' {
				masked[maskIndex] = ' '
			}
		}
		idx = end
	}
	return masked
}

type goCommentToken struct {
	Kind    token.Token
	Literal string
	Start   int
	End     int
}

func goCommentIsDelimiter(kind token.Token) bool {
	switch kind {
	case token.LPAREN, token.LBRACK, token.LBRACE, token.COMMA, token.PERIOD,
		token.RPAREN, token.RBRACK, token.RBRACE, token.SEMICOLON, token.COLON:
		return true
	default:
		return false
	}
}

func goCommentHasOperandSides(tokens []goCommentToken, operatorIndex int) bool {
	if operatorIndex == 0 || operatorIndex+1 == len(tokens) ||
		!goCommentIsOperand(tokens[operatorIndex-1].Kind) ||
		!goCommentIsOperand(tokens[operatorIndex+1].Kind) {
		return false
	}
	if goCommentIsAmbiguousTightOperator(tokens, operatorIndex) {
		return false
	}
	return true
}

func goCommentHasPostfixOperand(tokens []goCommentToken, operatorIndex int) bool {
	if operatorIndex == 0 || !goCommentIsOperand(tokens[operatorIndex-1].Kind) {
		return false
	}
	return operatorIndex+1 == len(tokens) || !goCommentIsWordLike(tokens[operatorIndex+1].Kind)
}

func goCommentHasUnaryRHSOperand(tokens []goCommentToken, operatorIndex int) bool {
	if operatorIndex == 0 || !goCommentIsOperand(tokens[operatorIndex-1].Kind) {
		return false
	}
	right := operatorIndex + 1
	if right == len(tokens) || !goCommentIsUnaryOperator(tokens[right].Kind) {
		return false
	}
	for right < len(tokens) && goCommentIsUnaryOperator(tokens[right].Kind) {
		right++
	}
	return right < len(tokens) && goCommentIsOperand(tokens[right].Kind)
}

func goCommentIsUnaryOperator(kind token.Token) bool {
	switch kind {
	case token.ADD, token.SUB, token.NOT, token.XOR, token.MUL, token.AND, token.ARROW, token.TILDE:
		return true
	default:
		return false
	}
}

func goCommentIsAmbiguousTightOperator(tokens []goCommentToken, operatorIndex int) bool {
	operator := tokens[operatorIndex]
	if operator.Kind != token.QUO && operator.Kind != token.AND && operator.Kind != token.SUB ||
		operatorIndex == 0 || operatorIndex+1 == len(tokens) {
		return false
	}
	left := tokens[operatorIndex-1]
	right := tokens[operatorIndex+1]
	if left.End != operator.Start || operator.End != right.Start || left.Kind != token.IDENT {
		return false
	}
	switch operator.Kind {
	case token.QUO, token.AND:
		return right.Kind == token.IDENT
	case token.SUB:
		if right.Kind == token.IDENT {
			return true
		}
		return right.Kind == token.INT && goCommentIsNetworkingWord(left.Literal)
	default:
		return false
	}
}

func goCommentHasAmbiguousTightOperator(tokens []goCommentToken) bool {
	for idx := range tokens {
		if goCommentIsAmbiguousTightOperator(tokens, idx) {
			return true
		}
	}
	return false
}

func goCommentIsOperand(kind token.Token) bool {
	return kind == token.IDENT || kind.IsLiteral()
}

func goCommentIsWordLike(kind token.Token) bool {
	return goCommentIsOperand(kind) || kind.IsKeyword()
}

func goCommentIsNetworkingWord(word string) bool {
	word = strings.ToLower(word)
	if strings.HasPrefix(word, "ipv") && len(word) > len("ipv") {
		for _, value := range word[len("ipv"):] {
			if value < '0' || value > '9' {
				return false
			}
		}
		return true
	}
	return word == "layer"
}

func isAllowedGoCommentOpening(word string, allowedWords map[string]struct{}) bool {
	_, found := allowedWords[word]
	return found
}
