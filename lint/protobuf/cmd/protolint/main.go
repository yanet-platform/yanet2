package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const modulePrefix = "github.com/yanet-platform/yanet2/"

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
	path       string // path to the file (may be absolute or relative to root)
	dir        string // base name of the containing directory
	pkg        string // value of the "package" statement
	goPkgLine  string // raw line containing "option go_package", empty if absent
	goPkgFound bool   // true if "option go_package" was encountered
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
		if !lintFile(f, ".") {
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
		// filepath.Rel returns a path without ".." prefix when path is inside ex.
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

// parseProtoFile reads a .proto file and extracts the package and go_package
// values.
func parseProtoFile(path string) (protoFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return protoFile{}, err
	}
	defer f.Close()

	pf := protoFile{
		path: path,
		dir:  filepath.Base(filepath.Dir(path)),
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// package statement
		if after, ok := strings.CutPrefix(line, "package "); ok {
			pf.pkg = strings.TrimSuffix(after, ";")
			continue
		}

		// go_package option — capture the raw line for later validation
		if strings.Contains(line, "option go_package") {
			pf.goPkgFound = true
			pf.goPkgLine = line
		}
	}

	return pf, scanner.Err()
}

// parseGoPkg parses the go_package line and returns (importPath, alias, ok).
// ok is false when the line is malformed.
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

// lintFile runs all checks against a parsed proto file and prints any
// violations. root is the directory relative to which the expected go_package
// path is computed. It returns true when the file passes all checks.
func lintFile(pf protoFile, root string) bool {
	ok := true

	// Rule 1: package name must match the containing directory name.
	if pf.pkg != pf.dir {
		fmt.Printf("%s: package %q does not match directory name %q\n",
			pf.path, pf.pkg, pf.dir)
		ok = false
	}

	// No go_package — skip further checks.
	if !pf.goPkgFound {
		return ok
	}

	line := pf.goPkgLine

	// Rule 2: go_package must be on a single line ending with ";".
	if !strings.HasSuffix(line, ";") {
		fmt.Printf("%s: option go_package must be written on a single line ending with \";\"\n",
			pf.path)
		return false
	}

	importPath, alias, parsed := parseGoPkg(line)
	if !parsed {
		fmt.Printf("%s: option go_package has malformed value\n", pf.path)
		return false
	}

	// Rule 3: alias must be present.
	if alias == "" {
		fmt.Printf("%s: option go_package must contain an alias (e.g. \"...;%s\")\n",
			pf.path, pf.dir)
		ok = false
	} else if alias != pf.pkg {
		// Rule 4: alias must equal the package name.
		fmt.Printf("%s: option go_package alias %q does not match package name %q\n",
			pf.path, alias, pf.pkg)
		ok = false
	}

	// Rule 5: import path must equal modulePrefix + dir of the proto file
	// relative to root.
	rel, err := filepath.Rel(root, pf.path)
	if err != nil {
		fmt.Printf("%s: cannot compute relative path from %q: %v\n", pf.path, root, err)
		return false
	}
	expectedPath := modulePrefix + filepath.ToSlash(filepath.Dir(rel))

	if importPath != expectedPath {
		fmt.Printf("%s: go_package path %q does not match expected %q\n",
			pf.path, importPath, expectedPath)
		ok = false
	}

	return ok
}
