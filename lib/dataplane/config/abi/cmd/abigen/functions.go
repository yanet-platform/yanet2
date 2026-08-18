package main

import (
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"strings"
)

type functionInfo struct {
	// Name is the function's linker symbol name, used as the manifest entity key.
	Name string
	// DeclPath is the function's defining translation unit, relative to repoRoot.
	DeclPath string
	// Return is the function's return type.
	//
	// It uses abigen's canonical type-descriptor syntax.
	Return string
	// Params holds the function's parameter types, in order.
	//
	// Each uses abigen's canonical type-descriptor syntax.
	Params []string
}

func extractFunctions(soPath, repoRoot string) ([]functionInfo, error) {
	f, err := elf.Open(soPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", soPath, err)
	}

	defer f.Close()

	data, err := f.DWARF()
	if err != nil {
		return nil, fmt.Errorf("failed to read DWARF from %s: %w", soPath, err)
	}

	definedNames, err := elfDefinedFunctionNames(f)
	if err != nil {
		return nil, err
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

		fn, ok, err := readSubprogramEntry(data, reader, entry, lineFiles, repoRoot, definedNames)
		if err != nil {
			return nil, err
		}

		if !ok {
			continue
		}

		if existing, dup := byName[fn.Name]; dup {
			if !sameFunction(existing, fn) {
				return nil, fmt.Errorf(
					"conflicting definitions for function %s (%s vs %s)",
					fn.Name, existing.DeclPath, fn.DeclPath,
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

func elfDefinedFunctionNames(f *elf.File) (map[string]bool, error) {
	names := map[string]bool{}
	collect := func(syms []elf.Symbol) {
		for _, s := range syms {
			if s.Name == "" || s.Section == elf.SHN_UNDEF {
				continue
			}

			if elf.ST_TYPE(s.Info) != elf.STT_FUNC {
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

func functionDeclPath(abs, repoRoot string) (string, bool) {
	rel, ok := repoRelative(abs, repoRoot)
	if !ok || rel == "" {
		return "", false
	}

	return rel, true
}

type subprogramFacts struct {
	// IsDeclaration is true for a prototype-only DWARF entry.
	//
	// A definition is required, so this always rejects.
	IsDeclaration bool
	// HasOwnName is true when the entry carries its own name.
	//
	// It is false when the name is only reachable via abstract origin.
	HasOwnName bool
	// OriginAvailable is true once a name has been resolved.
	//
	// The name comes either from the entry itself (HasOwnName) or, for
	// an unnamed concrete instance, from resolving its DWARF abstract
	// origin (the gcc/clang scheme for inlined and folded functions).
	OriginAvailable bool
	// OriginExternal mirrors the resolved origin's external linkage.
	//
	// False means the function is static, which is always rejected
	// regardless of the other facts.
	OriginExternal bool
	// OriginName is the resolved origin's function name.
	//
	// It is empty until OriginAvailable is true.
	OriginName string
	// DefinedInSymtab is true when OriginName has a defined ELF symbol.
	//
	// This confirms the origin resolved a name that a defining function
	// symbol actually backs in the probe's own ELF, rejecting a
	// declaration-only prototype whose name never got a definition.
	DefinedInSymtab bool
}

func classifySubprogram(f subprogramFacts) bool {
	if f.IsDeclaration {
		return false
	}

	if !f.HasOwnName && !f.OriginAvailable {
		return false
	}

	return f.OriginExternal && f.OriginName != "" && f.DefinedInSymtab
}

func readSubprogramEntry(
	data *dwarf.Data, reader *dwarf.Reader, entry *dwarf.Entry, lineFiles []*dwarf.LineFile, repoRoot string,
	definedNames map[string]bool,
) (functionInfo, bool, error) {
	isDeclaration := entry.Val(dwarf.AttrDeclaration) != nil
	hasOwnName := entry.Val(dwarf.AttrName) != nil

	origin := entry
	originReader := reader
	originAvailable := hasOwnName
	if !isDeclaration && !hasOwnName {
		resolved, resolvedReader, err := resolveAbstractOrigin(data, entry)
		if err != nil {
			if entry.Children {
				reader.SkipChildren()
			}

			return functionInfo{}, false, err
		}

		if resolved != nil {
			origin = resolved
			originReader = resolvedReader
			originAvailable = true
		}
	}

	var originExternal bool
	var name string
	if originAvailable {
		originExternal, _ = origin.Val(dwarf.AttrExternal).(bool)
		name, _ = origin.Val(dwarf.AttrName).(string)
	}

	accept := classifySubprogram(subprogramFacts{
		IsDeclaration:   isDeclaration,
		HasOwnName:      hasOwnName,
		OriginAvailable: originAvailable,
		OriginExternal:  originExternal,
		OriginName:      name,
		DefinedInSymtab: definedNames[name],
	})
	if !accept {
		if entry.Children {
			reader.SkipChildren()
		}

		return functionInfo{}, false, nil
	}

	declFile, ok := origin.Val(dwarf.AttrDeclFile).(int64)
	if !ok || declFile < 0 || int(declFile) >= len(lineFiles) || lineFiles[declFile] == nil {
		if entry.Children {
			reader.SkipChildren()
		}

		return functionInfo{}, false, nil
	}

	declPath, ok := functionDeclPath(lineFiles[declFile].Name, repoRoot)
	if !ok {
		if entry.Children {
			reader.SkipChildren()
		}

		return functionInfo{}, false, nil
	}

	retType, err := resolveTypeAttr(data, origin)
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

	params, err := readFormalParameters(data, originReader, origin)
	if err != nil {
		return functionInfo{}, false, fmt.Errorf("failed to describe parameters of %s: %w", name, err)
	}

	if origin != entry && entry.Children {
		reader.SkipChildren()
	}

	return functionInfo{Name: name, DeclPath: declPath, Return: ret, Params: params}, true, nil
}

func resolveAbstractOrigin(data *dwarf.Data, entry *dwarf.Entry) (*dwarf.Entry, *dwarf.Reader, error) {
	val := entry.Val(dwarf.AttrAbstractOrigin)
	if val == nil {
		return nil, nil, nil
	}

	off, ok := val.(dwarf.Offset)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported AttrAbstractOrigin value %T at DWARF offset %d", val, entry.Offset)
	}

	originReader := data.Reader()
	originReader.Seek(off)
	origin, err := originReader.Next()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read DWARF entry at offset %d: %w", off, err)
	}

	return origin, originReader, nil
}

func resolveTypeAttr(data *dwarf.Data, entry *dwarf.Entry) (dwarf.Type, error) {
	val := entry.Val(dwarf.AttrType)
	if val == nil {
		return nil, nil
	}

	off, ok := val.(dwarf.Offset)
	if !ok {
		return nil, fmt.Errorf("unsupported AttrType value %T at DWARF offset %d", val, entry.Offset)
	}

	return data.Type(off)
}

func readFormalParameters(data *dwarf.Data, reader *dwarf.Reader, entry *dwarf.Entry) ([]string, error) {
	if !entry.Children {
		return nil, nil
	}

	var params []string
	depth := 0
	for {
		kid, err := reader.Next()
		if err != nil {
			return nil, fmt.Errorf("failed to read DWARF entries: %w", err)
		}

		if kid == nil {
			return nil, fmt.Errorf("unexpected end of DWARF entries while reading formal parameters")
		}

		if kid.Tag == 0 {
			if depth == 0 {
				return params, nil
			}

			depth--
			continue
		}

		if depth == 0 {
			switch kid.Tag {
			case dwarf.TagFormalParameter:
				typ, err := resolveTypeAttr(data, kid)
				if err != nil {
					return nil, err
				}

				desc, err := describeType(typ)
				if err != nil {
					return nil, err
				}

				params = append(params, desc)
			case dwarf.TagUnspecifiedParameters:
				params = append(params, "...")
			}
		}

		if kid.Children {
			depth++
		}
	}
}

func sameFunction(a, b functionInfo) bool {
	if a.Name != b.Name || a.Return != b.Return || len(a.Params) != len(b.Params) {
		return false
	}

	for idx := range a.Params {
		if a.Params[idx] != b.Params[idx] {
			return false
		}
	}

	return true
}

func functionLine(fn functionInfo) string {
	return fmt.Sprintf("fn %s %s(%s)", fn.Name, fn.Return, strings.Join(fn.Params, ","))
}
