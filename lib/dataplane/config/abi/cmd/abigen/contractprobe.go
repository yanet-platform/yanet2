package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func entryMacros(entry compileEntry) (map[string]string, error) {
	tokens, err := shellSplit(entry.Command)
	if err != nil {
		return nil, err
	}

	macros := map[string]string{}
	for _, tok := range tokens {
		if !strings.HasPrefix(tok, "-D") {
			continue
		}

		name, value, _ := strings.Cut(tok[len("-D"):], "=")
		macros[name] = value
	}

	return macros, nil
}

func selectContractTemplate(entries []compileEntry) (compileEntry, error) {
	perEntry := make([]map[string]string, len(entries))
	for idx, entry := range entries {
		macros, err := entryMacros(entry)
		if err != nil {
			return compileEntry{}, err
		}

		perEntry[idx] = macros
	}

	valuesByMacro := map[string]map[string][]string{}
	for idx, macros := range perEntry {
		unit := resolveTUPath(entries[idx])
		for name, value := range macros {
			if valuesByMacro[name] == nil {
				valuesByMacro[name] = map[string][]string{}
			}

			valuesByMacro[name][value] = append(valuesByMacro[name][value], unit)
		}
	}

	names := make([]string, 0, len(valuesByMacro))
	for name := range valuesByMacro {
		names = append(names, name)
	}

	sort.Strings(names)
	for _, name := range names {
		byValue := valuesByMacro[name]
		if len(byValue) < 2 {
			continue
		}

		values := make([]string, 0, len(byValue))
		for value := range byValue {
			values = append(values, value)
		}

		sort.Strings(values)
		variants := make([]string, 0, len(values))
		for _, value := range values {
			units := byValue[value]
			sort.Strings(units)
			variants = append(variants, fmt.Sprintf("%q (%s)", value, strings.Join(units, ", ")))
		}

		return compileEntry{}, fmt.Errorf(
			"discovered translation units disagree on -D%s: %s; the contract header probe needs one consistent definition",
			name, strings.Join(variants, " vs "),
		)
	}

	if err := verifyTemplateMacroSubset(entries, perEntry, 0); err != nil {
		return compileEntry{}, err
	}

	return entries[0], nil
}

func verifyTemplateMacroSubset(entries []compileEntry, perEntry []map[string]string, templateIdx int) error {
	template := perEntry[templateIdx]
	names := make([]string, 0, len(template))
	for name := range template {
		names = append(names, name)
	}

	sort.Strings(names)
	for _, name := range names {
		value := template[name]
		var missing []string
		for idx, macros := range perEntry {
			if idx == templateIdx {
				continue
			}

			if got, ok := macros[name]; !ok || got != value {
				missing = append(missing, resolveTUPath(entries[idx]))
			}
		}

		if len(missing) == 0 {
			continue
		}

		sort.Strings(missing)
		return fmt.Errorf(
			"contract probe template %s defines -D%s=%q, not matched by: %s; the contract header probe needs one consistent definition",
			resolveTUPath(entries[templateIdx]), name, value, strings.Join(missing, ", "),
		)
	}

	return nil
}

func contractHeaderProbeSource(headers []string) string {
	var b strings.Builder
	for _, rel := range headers {
		fmt.Fprintf(&b, "#include \"%s\"\n", rel)
	}

	return b.String()
}

func buildContractHeaderProbe(
	templateEntry compileEntry, ccOverride, workDir string, headers []string,
) (soPath string, err error) {
	src := filepath.Join(workDir, "contract_probe.c")
	if err := os.WriteFile(src, []byte(contractHeaderProbeSource(headers)), 0o644); err != nil {
		return "", fmt.Errorf("failed to write contract header probe source: %w", err)
	}

	headerEntry := compileEntry{Directory: templateEntry.Directory, Command: templateEntry.Command, File: src}

	probeDir := filepath.Join(workDir, "contract")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create contract header work directory: %w", err)
	}

	objPaths, linkCompiler, linkFlags, err := compileTUs([]compileEntry{headerEntry}, ccOverride, probeDir, true)
	if err != nil {
		return "", err
	}

	soPath, err = linkSharedObject(linkCompiler, linkFlags, objPaths, probeDir)
	if err != nil {
		return "", err
	}

	return soPath, nil
}
