package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// semverRe matches a version-like suffix: v followed by a digit, then anything.
var semverRe = regexp.MustCompile(`^v[0-9]`)

// excludeList is a flag.Value that accumulates multiple --exclude values.
type excludeList []string

func (e *excludeList) String() string {
	return strings.Join(*e, ", ")
}

func (e *excludeList) Set(v string) error {
	*e = append(*e, filepath.Clean(v))
	return nil
}

// protoFile holds parsed data from a single .proto file.
type protoFile struct {
	Path       string // path to the file (may be absolute or relative to root)
	GoPkgLine  string // raw line containing "option go_package", empty if absent
	GoPkgFound bool   // true if "option go_package" was encountered
}

func main() {
	var excludes excludeList
	flag.Var(&excludes, "exclude", "directory to exclude (may be repeated)")
	flag.Parse()

	files, err := collectProtoFiles(".", excludes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error walking directory: %v\n", err)
		os.Exit(1)
	}

	var failed bool
	for _, f := range files {
		if !lintFile(f) {
			failed = true
		}
	}

	if failed {
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

// collectProtoFiles walks the directory tree rooted at root and returns a
// parsed protoFile for every .proto file found, skipping excluded directories.
func collectProtoFiles(root string, excludes []string) ([]protoFile, error) {
	var files []protoFile

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			if isExcluded(path, excludes) {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".proto") {
			return nil
		}

		pf, err := parseProtoFile(path)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		files = append(files, pf)
		return nil
	})

	return files, err
}

// parseProtoFile reads a .proto file and extracts the go_package option.
func parseProtoFile(path string) (protoFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return protoFile{}, err
	}
	defer f.Close()

	pf := protoFile{
		Path: path,
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// go_package option — capture the raw line for later validation
		if strings.Contains(line, "option go_package") {
			pf.GoPkgFound = true
			pf.GoPkgLine = line
		}
	}

	return pf, scanner.Err()
}

// parseGoPkg parses the go_package line and returns (importPath, alias, ok). ok
// is false when the line is malformed.
func parseGoPkg(line string) (importPath, alias string, ok bool) {
	start := strings.Index(line, `"`)
	end := strings.LastIndex(line, `"`)
	if start == -1 || end == -1 || end <= start {
		return "", "", false
	}

	raw := line[start+1 : end]
	parts := strings.SplitN(raw, ";", 2)
	if len(parts) != 2 {
		return parts[0], "", true
	}
	return parts[0], parts[1], true
}

// isSemver reports whether s looks like a Go module major version suffix (e.g.
// "v1", "v2", "v1beta1").
func isSemver(s string) bool {
	return semverRe.MatchString(s)
}

// penultimateSegment returns the second-to-last path segment of importPath. For
// "github.com/org/repo/aclpb/v1" it returns "aclpb". Returns empty string if
// there are fewer than 2 segments.
func penultimateSegment(importPath string) string {
	parts := strings.Split(importPath, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

// lintFile runs go_package checks against a parsed proto file and prints any
// violations. It returns true when the file passes all checks.
func lintFile(pf protoFile) bool {
	// No go_package — nothing to check.
	if !pf.GoPkgFound {
		return true
	}

	ok := true
	line := pf.GoPkgLine

	// Rule 1: go_package must be on a single line ending with ";".
	if !strings.HasSuffix(line, ";") {
		fmt.Printf("%s: option go_package must be written on a single line ending with \";\"\n", pf.Path)
		return false
	}

	importPath, alias, parsed := parseGoPkg(line)
	if !parsed {
		fmt.Printf("%s: option go_package has malformed value\n", pf.Path)
		return false
	}

	// Rule 2: alias must be present.
	if alias == "" {
		fmt.Printf("%s: option go_package must contain an alias (e.g. \"path;alias\")\n", pf.Path)
		ok = false
	} else {
		// Rule 3: alias must equal the penultimate directory in the import
		// path. For "github.com/.../aclpb/v1;aclpb", penultimate is "aclpb".
		penult := penultimateSegment(importPath)
		if penult != "" && alias != penult {
			fmt.Printf("%s: option go_package alias %q does not match penultimate path segment %q\n", pf.Path, alias, penult)
			ok = false
		}
	}

	// Rule 4: the last segment of the import path must be a version (e.g. "v1",
	// "v2").
	parts := strings.Split(importPath, "/")
	lastSegment := parts[len(parts)-1]
	if !isSemver(lastSegment) {
		fmt.Printf("%s: last segment of go_package path %q is not a valid version (expected v1, v2, ...)\n", pf.Path, lastSegment)
		ok = false
	}

	return ok
}
