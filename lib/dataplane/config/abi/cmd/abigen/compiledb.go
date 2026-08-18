package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

type compileEntry struct {
	// Directory is the working directory the compile command runs from.
	Directory string `json:"directory"`
	// Command is the compiler invocation as a single shell-quoted string.
	Command string `json:"command"`
	// File is the source path, absolute or relative to Directory.
	File string `json:"file"`
	// Output is the object file path, as recorded by the build system.
	Output string `json:"output"`
}

type compileDB map[string]compileEntry

func loadCompileDB(path string) (compileDB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read compile database: %w", err)
	}

	return parseCompileDB(data)
}

func parseCompileDB(data []byte) (compileDB, error) {
	var entries []compileEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse compile database: %w", err)
	}

	db := make(compileDB, len(entries))
	for _, entry := range entries {
		abs := entry.File
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(entry.Directory, entry.File)
		}

		key := filepath.Clean(abs)
		if existing, dup := db[key]; dup && !preferReplacementEntry(existing, entry) {
			continue
		}

		db[key] = entry
	}

	return db, nil
}

func preferReplacementEntry(existing, replacement compileEntry) bool {
	existingPIC := isPositionIndependent(existing.Command)
	replacementPIC := isPositionIndependent(replacement.Command)
	if existingPIC != replacementPIC {
		return replacementPIC
	}

	return replacement.Command > existing.Command
}

func isPositionIndependent(command string) bool {
	tokens, err := shellSplit(command)
	if err != nil {
		return false
	}

	return slices.Contains(tokens, "-fPIC")
}

func (m compileDB) Lookup(absPath string) (compileEntry, error) {
	entry, ok := m[filepath.Clean(absPath)]
	if !ok {
		return compileEntry{}, fmt.Errorf("no compile command found for %s", absPath)
	}

	return entry, nil
}
