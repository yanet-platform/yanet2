package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const defaultOptFlag = "-O2"

var pairedKeepFlags = map[string]bool{
	"-I":                 true,
	"-D":                 true,
	"-U":                 true,
	"-A":                 true,
	"-imultilib":         true,
	"-include":           true,
	"-imacros":           true,
	"-isystem":           true,
	"-iquote":            true,
	"-idirafter":         true,
	"-iprefix":           true,
	"-iwithprefix":       true,
	"-iwithprefixbefore": true,
	"-isysroot":          true,
	"--sysroot":          true,
	"-x":                 true,
	"-target":            true,
	"-arch":              true,
	"-gcc-toolchain":     true,
	"-Xclang":            true,
	"-Xpreprocessor":     true,
	"-Xassembler":        true,
	"-Xlinker":           true,
}

var pairedDropFlags = map[string]bool{
	"-MQ": true,
	"-MF": true,
	"-o":  true,
}

var bareDropFlags = map[string]bool{
	"-MD": true,
	"-c":  true,
}

var compilerLaunchers = map[string]bool{
	"ccache":  true,
	"sccache": true,
}

var pathRemapFlags = []string{
	"-ffile-prefix-map",
	"-fdebug-prefix-map",
	"-fmacro-prefix-map",
	"-fdebug-compilation-dir",
}

func isPathRemapJoinedFlag(tok string) bool {
	for _, f := range pathRemapFlags {
		if strings.HasPrefix(tok, f+"=") {
			return true
		}
	}

	return false
}

func isPathRemapSeparatedFlag(tok string) bool {
	return slices.Contains(pathRemapFlags, tok)
}

var linkDriverKeepFlags = map[string]bool{
	"-target":        true,
	"--sysroot":      true,
	"-isysroot":      true,
	"-arch":          true,
	"-gcc-toolchain": true,
}

var linkDriverJoinedPrefixes = map[string][]string{
	"-target":        {"--target"},
	"-gcc-toolchain": {"--gcc-toolchain"},
}

func linkDriverJoinedFlag(tok string) bool {
	for f := range linkDriverKeepFlags {
		if strings.HasPrefix(tok, f+"=") {
			return true
		}

		for _, alt := range linkDriverJoinedPrefixes[f] {
			if strings.HasPrefix(tok, alt+"=") {
				return true
			}
		}
	}

	return false
}

func linkDriverFlags(args []string) []string {
	var out []string
	for idx := 0; idx < len(args); idx++ {
		if linkDriverJoinedFlag(args[idx]) {
			out = append(out, args[idx])
			continue
		}

		if !linkDriverKeepFlags[args[idx]] {
			continue
		}

		out = append(out, args[idx])
		if idx+1 < len(args) {
			out = append(out, args[idx+1])
			idx++
		}
	}

	return out
}

func transformCompileArgs(
	tokens []string, ccOverride string, keepUnusedDebugTypes bool,
) (compiler string, args []string, err error) {
	if len(tokens) == 0 {
		return "", nil, fmt.Errorf("failed to transform compile command: command is empty")
	}

	start := 1
	compiler = tokens[0]
	if compilerLaunchers[filepath.Base(tokens[0])] {
		if len(tokens) < 2 {
			return "", nil, fmt.Errorf("failed to transform compile command: launcher %s has no compiler after it", tokens[0])
		}

		compiler = tokens[1]
		start = 2
	}

	if ccOverride != "" {
		compiler = ccOverride
	}

	var out []string
	for idx := start; idx < len(tokens); idx++ {
		tok := tokens[idx]
		switch {
		case isPathRemapJoinedFlag(tok):
			continue
		case isPathRemapSeparatedFlag(tok):
			if idx+1 >= len(tokens) {
				return "", nil, fmt.Errorf("failed to transform compile command: %s is missing its argument", tok)
			}

			idx++
		case pairedKeepFlags[tok]:
			if idx+1 >= len(tokens) {
				return "", nil, fmt.Errorf("failed to transform compile command: %s is missing its argument", tok)
			}

			out = append(out, tok, tokens[idx+1])
			idx++
		case pairedDropFlags[tok]:
			if idx+1 >= len(tokens) {
				return "", nil, fmt.Errorf("failed to transform compile command: %s is missing its argument", tok)
			}

			idx++
		case bareDropFlags[tok]:
			continue
		case strings.HasPrefix(tok, "-g"):
			continue
		case strings.HasPrefix(tok, "-O"):
			continue
		case strings.HasPrefix(tok, "-"):
			out = append(out, tok)
		default:
			continue
		}
	}

	out = append(out, "-gdwarf-4")
	if keepUnusedDebugTypes {
		out = append(out, "-fno-eliminate-unused-debug-types")
	}

	out = append(out, "-fPIC", defaultOptFlag)
	return compiler, out, nil
}
