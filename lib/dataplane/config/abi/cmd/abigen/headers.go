package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

var headerTree = map[string]bool{
	"lib":       true,
	"common":    true,
	"api":       true,
	"dataplane": true,
}

type headerScan struct {
	// DepfileInputs lists every header scanHeaders found.
	//
	// It feeds the depfile abigen writes on request.
	DepfileInputs []string
}

func scanHeaders(entries []compileEntry, ccOverride string) (headerScan, error) {
	depSeen := map[string]bool{}
	for _, entry := range entries {
		depSeen[resolveTUPath(entry)] = true
		deps, err := headerDeps(entry, ccOverride)
		if err != nil {
			return headerScan{}, err
		}

		for _, dep := range deps {
			abs := dep
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(entry.Directory, dep)
			}

			depSeen[filepath.Clean(abs)] = true
		}
	}

	deps := make([]string, 0, len(depSeen))
	for abs := range depSeen {
		deps = append(deps, abs)
	}

	sort.Strings(deps)

	return headerScan{DepfileInputs: deps}, nil
}

func discoverContractHeaders(repoRoot string) ([]string, error) {
	trees := make([]string, 0, len(headerTree))
	for tree := range headerTree {
		trees = append(trees, tree)
	}

	sort.Strings(trees)

	var headers []string
	for _, tree := range trees {
		root := filepath.Join(repoRoot, tree)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() || filepath.Ext(path) != ".h" {
				return nil
			}

			rel, ok := repoRelative(path, repoRoot)
			if !ok {
				return nil
			}

			headers = append(headers, rel)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to scan %s for contract headers: %w", tree, err)
		}
	}

	sort.Strings(headers)
	return headers, nil
}

func typeDeclPath(abs, repoRoot string) (string, bool) {
	rel, ok := repoRelative(abs, repoRoot)
	if !ok {
		return "", false
	}

	if filepath.Ext(rel) == ".c" {
		return "", false
	}

	top := strings.SplitN(rel, "/", 2)[0]
	if !headerTree[top] {
		return "", false
	}

	return rel, true
}

var systemHeaderPrefixes = []string{"/usr/include/", "/usr/lib/gcc/", "/usr/lib64/gcc/"}

func looksLikeContractHeaderPath(abs string) bool {
	abs = filepath.ToSlash(abs)
	for _, prefix := range systemHeaderPrefixes {
		if strings.HasPrefix(abs, prefix) {
			return false
		}
	}

	for part := range strings.SplitSeq(abs, "/") {
		if headerTree[part] {
			return true
		}
	}

	return false
}

func repoRelative(abs, repoRoot string) (string, bool) {
	rel, err := filepath.Rel(repoRoot, abs)
	if err != nil {
		return "", false
	}

	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") || rel == ".." {
		return "", false
	}

	return rel, true
}
