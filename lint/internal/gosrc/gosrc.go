// Package gosrc walks the Go source tree, skipping directories that no
// linter in this repository should ever descend into.
package gosrc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// generatedRe matches the standard "generated code" marker comment.
var generatedRe = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// nodeModulesName is the node_modules directory basename, skipped wherever
// it appears in the tree since JavaScript tooling nests it arbitrarily
// deeply (e.g. web/node_modules).
const nodeModulesName = "node_modules"

// rootRelativeExcludes are directories skipped by their path relative to
// the scan root, rather than by basename, so that a same-named directory
// elsewhere in the tree is still scanned.
//
// "tests" is the top-level QEMU functional-test harness, out of scope for
// this repository's Go linters per an explicit project decision.
// "subprojects" holds the DPDK meson subproject. The "build*" entries are
// the meson build directories created by "meson setup". They are matched
// exactly, not by prefix, so that a future Go package such as "builder/" or
// "buildinfo/" is not silently exempted.
var rootRelativeExcludes = map[string]bool{
	"tests":       true,
	"subprojects": true,
	"build":       true,
	"build-asan":  true,
	"build-tsan":  true,
	"build-perf":  true,
}

// ExcludeList is a flag.Value that accumulates multiple --exclude values.
type ExcludeList []string

func (m *ExcludeList) String() string {
	return strings.Join(*m, ", ")
}

func (m *ExcludeList) Set(v string) error {
	*m = append(*m, filepath.Clean(v))
	return nil
}

// Excluded reports whether path is inside any of the excluded directories.
func (m ExcludeList) Excluded(path string) bool {
	for _, ex := range m {
		// The filepath.Rel call returns a path without ".." prefix when path is
		// inside ex.
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

// Walk walks the directory tree rooted at root and calls visit for every
// .go file found in a non-excluded, non-git-ignored directory.
//
// It skips dot- and underscore-prefixed directories (except root itself),
// node_modules, the root-relative excludes, and any directory listed in
// excludes. It does not filter files beyond the .go suffix and the
// git-ignore check below: callers that need to skip generated files or
// _test.go files must do so in visit.
func Walk(root string, excludes ExcludeList, visit func(path string) error) error {
	var candidates []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			name := info.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			if name == nodeModulesName {
				return filepath.SkipDir
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil && rootRelativeExcludes[rel] {
				return filepath.SkipDir
			}
			if excludes.Excluded(path) {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		candidates = append(candidates, path)
		return nil
	})
	if err != nil {
		return err
	}

	// A tracked debt ledger must never reference a private, git-ignored
	// tree: a public clone lacks the file, so its ledger row would be
	// permanently stale, and the private tree's name would leak into a
	// public file. Batching the check into a single "git check-ignore"
	// call keeps the walk fast even on a large tree.
	ignored, ignoreErr := gitIgnored(candidates)
	if ignoreErr != nil {
		ignored = nil
	}

	for _, path := range candidates {
		if ignored[path] {
			continue
		}
		if err := visit(path); err != nil {
			return err
		}
	}

	return nil
}

// gitIgnored batch-checks paths against git's exclude mechanism and returns
// the subset that git ignores.
//
// It reports an error only when git is unavailable or the invocation
// genuinely fails. Callers should treat that as "nothing is ignored" rather
// than failing the whole scan.
func gitIgnored(paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	var stdin bytes.Buffer
	for _, path := range paths {
		stdin.WriteString(path)
		stdin.WriteByte(0)
	}

	cmd := exec.Command("git", "check-ignore", "-z", "--stdin")
	cmd.Stdin = &stdin
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	runErr := cmd.Run()
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return nil, fmt.Errorf("failed to run git check-ignore: %w", runErr)
		}
		// Exit code 1 means none of the candidates are ignored, which is
		// success with an empty result, not an error. Exit code 2 or
		// higher is a real failure (e.g. not inside a git repository).
		if exitErr.ExitCode() >= 2 {
			return nil, fmt.Errorf("failed to run git check-ignore: %w", runErr)
		}
	}

	ignored := map[string]bool{}
	for path := range strings.SplitSeq(stdout.String(), "\x00") {
		if path != "" {
			ignored[path] = true
		}
	}

	return ignored, nil
}

// IsGenerated reports whether content carries the standard "generated code"
// marker comment before its package clause.
func IsGenerated(content []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if generatedRe.MatchString(line) {
			return true
		}
		if strings.HasPrefix(strings.TrimSpace(line), "package ") {
			return false
		}
	}
	return false
}
