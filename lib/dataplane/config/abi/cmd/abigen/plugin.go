package main

import (
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
)

func buildPluginProbe(entries []compileEntry, ccOverride, workDir string) (soPath string, err error) {
	pluginDir := filepath.Join(workDir, "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create plugin work directory: %w", err)
	}

	objPaths, linkCompiler, linkFlags, err := compileTUs(entries, ccOverride, pluginDir, true)
	if err != nil {
		return "", err
	}

	soPath, err = linkSharedObject(linkCompiler, linkFlags, objPaths, pluginDir)
	if err != nil {
		return "", err
	}

	return soPath, nil
}

func elfUndefinedFunctionNames(f *elf.File) (map[string]bool, error) {
	names := map[string]bool{}
	collect := func(syms []elf.Symbol) {
		for _, s := range syms {
			if s.Name == "" || s.Section != elf.SHN_UNDEF {
				continue
			}

			typ := elf.ST_TYPE(s.Info)
			if typ != elf.STT_FUNC && typ != elf.STT_NOTYPE {
				continue
			}

			names[s.Name] = true
		}
	}

	syms, err := f.Symbols()
	if err != nil && err != elf.ErrNoSymbols {
		return nil, fmt.Errorf("failed to read ELF symbols: %w", err)
	}

	collect(syms)
	dynSyms, err := f.DynamicSymbols()
	if err != nil && err != elf.ErrNoSymbols {
		return nil, fmt.Errorf("failed to read ELF dynamic symbols: %w", err)
	}

	collect(dynSyms)
	return names, nil
}

func extractPluginFunctions(soPath, repoRoot string) ([]functionInfo, error) {
	f, err := elf.Open(soPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", soPath, err)
	}

	defer f.Close()

	undefined, err := elfUndefinedFunctionNames(f)
	if err != nil {
		return nil, err
	}

	data, err := f.DWARF()
	if err != nil {
		return nil, fmt.Errorf("failed to read DWARF from %s: %w", soPath, err)
	}

	byName := map[string]functionInfo{}
	reader := data.Reader()
	var lineFiles []*dwarf.LineFile
	for {
		entry, err := reader.Next()
		if err != nil {
			return nil, fmt.Errorf("failed to read DWARF entries: %w", err)
		}

		if entry == nil {
			break
		}

		if entry.Tag == dwarf.TagCompileUnit {
			lineFiles, err = compileUnitFiles(data, entry)
			if err != nil {
				return nil, err
			}

			continue
		}

		if entry.Tag != dwarf.TagSubprogram {
			continue
		}

		name, _ := entry.Val(dwarf.AttrName).(string)
		if name == "" || !undefined[name] {
			if entry.Children {
				reader.SkipChildren()
			}

			continue
		}

		fn, ok, err := readPluginFunctionDeclaration(data, reader, entry, lineFiles, repoRoot)
		if err != nil {
			return nil, err
		}

		if !ok {
			continue
		}

		if existing, dup := byName[fn.Name]; dup {
			if !sameFunction(existing, fn) {
				return nil, fmt.Errorf(
					"conflicting declarations for function %s (%s vs %s)", fn.Name, existing.DeclPath, fn.DeclPath,
				)
			}

			continue
		}

		byName[fn.Name] = fn
	}

	functions := make([]functionInfo, 0, len(byName))
	for _, fn := range byName {
		functions = append(functions, fn)
	}

	return functions, nil
}

func readPluginFunctionDeclaration(
	data *dwarf.Data, reader *dwarf.Reader, entry *dwarf.Entry, lineFiles []*dwarf.LineFile, repoRoot string,
) (functionInfo, bool, error) {
	name, _ := entry.Val(dwarf.AttrName).(string)
	declFile, ok := entry.Val(dwarf.AttrDeclFile).(int64)
	if !ok || declFile < 0 || int(declFile) >= len(lineFiles) || lineFiles[declFile] == nil {
		if entry.Children {
			reader.SkipChildren()
		}

		return functionInfo{}, false, nil
	}

	declPath, ok := typeDeclPath(lineFiles[declFile].Name, repoRoot)
	if !ok {
		if entry.Children {
			reader.SkipChildren()
		}

		return functionInfo{}, false, nil
	}

	retType, err := resolveTypeAttr(data, entry)
	if err != nil {
		if entry.Children {
			reader.SkipChildren()
		}

		return functionInfo{}, false, err
	}

	ret, err := describeType(retType)
	if err != nil {
		if entry.Children {
			reader.SkipChildren()
		}

		return functionInfo{}, false, fmt.Errorf("failed to describe return type of %s: %w", name, err)
	}

	params, err := readFormalParameters(data, reader, entry)
	if err != nil {
		return functionInfo{}, false, fmt.Errorf("failed to describe parameters of %s: %w", name, err)
	}

	return functionInfo{Name: name, DeclPath: declPath, Return: ret, Params: params}, true, nil
}
