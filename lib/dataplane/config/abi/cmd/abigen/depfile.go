package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const abigenPackageRelDir = "lib/dataplane/config/abi/cmd/abigen"

func filterPackageGoSources(names []string) []string {
	var sources []string
	for _, name := range names {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		sources = append(sources, name)
	}

	sort.Strings(sources)
	return sources
}

func abigenPackageInputs(repoRoot string) ([]string, error) {
	pkgDir := filepath.Join(repoRoot, abigenPackageRelDir)
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list abigen's own package directory %s: %w", pkgDir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		names = append(names, entry.Name())
	}

	sources := filterPackageGoSources(names)
	paths := make([]string, 0, len(sources)+1)
	for _, name := range sources {
		paths = append(paths, filepath.Join(pkgDir, name))
	}

	return append(paths, pkgDir), nil
}

func escapeDepfilePath(path string) string {
	return strings.ReplaceAll(path, " ", "\\ ")
}

func formatDepfile(target string, inputs []string) string {
	var b strings.Builder
	b.WriteString(escapeDepfilePath(target))
	b.WriteByte(':')
	for _, input := range inputs {
		b.WriteString(" \\\n  ")
		b.WriteString(escapeDepfilePath(input))
	}

	b.WriteByte('\n')
	return b.String()
}

func writeDepfile(path, target string, inputs []string) error {
	if err := os.WriteFile(path, []byte(formatDepfile(target, inputs)), 0o644); err != nil {
		return fmt.Errorf("failed to write depfile: %w", err)
	}

	return nil
}

func depfileTarget(cfg config) string {
	for _, candidate := range []string{cfg.EmitC, cfg.ManifestOut} {
		if candidate != "" {
			return candidate
		}
	}

	return "yanet-abi-manifest"
}
