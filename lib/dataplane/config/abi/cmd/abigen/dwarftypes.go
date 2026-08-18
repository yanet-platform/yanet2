package main

import (
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"slices"
	"sort"
	"strings"
)

type bitfieldScheme int

const (
	bitfieldSchemeLegacy bitfieldScheme = iota
	bitfieldSchemeDataBitOffset
)

type typeMember struct {
	// Name is the member's name.
	//
	// It is dotted for a flattened anonymous nested struct or union.
	Name string
	// Type is the member's type, in abigen's canonical type-descriptor syntax.
	Type string
	// ByteOffset is the member's offset from the start of the enclosing type.
	ByteOffset int64
	// BitSize is the member's bit-field width, zero for an ordinary member.
	BitSize int64
	// DataBitOffset is the member's bit offset from the enclosing type.
	//
	// It is set only when BitSize is nonzero, and already normalized
	// across bitfieldScheme by normalizedDataBitOffset.
	DataBitOffset int64
}

type enumConstant struct {
	// Name is the enumerator's identifier.
	Name string
	// Value is the enumerator's integer value.
	Value int64
}

type typeInfo struct {
	// Kind is "struct", "union", "enum" or "typedef".
	//
	// It forms half of the manifest key alongside Name. "typedef" keys a
	// struct or union that is anonymous but named through its typedef, kept
	// apart from the "struct"/"union" tag namespace to avoid a name clash.
	Kind string
	// Name is the type's tag name.
	//
	// It is a synthesized "anon@..." name for an anonymous enum, or the
	// typedef name for a Kind == "typedef" entry.
	Name string
	// ByteSize is the type's size, as DWARF reports it.
	ByteSize int64
	// Alignment is the struct or union's DW_AT_alignment, 0 when absent.
	//
	// A real alignment is never 0, so 0 unambiguously means "not set".
	Alignment int64
	// DeclPath is the header the type is declared in, relative to repoRoot.
	DeclPath string
	// Members holds the type's fields in declaration order, empty for an enum.
	Members []typeMember
	// Enumerators holds the type's named values in declaration order.
	//
	// It is empty for a struct or union.
	Enumerators []enumConstant
}

const unresolvedDeclPathSampleSize = 3

type unresolvedDeclSample struct {
	// Paths holds a bounded, deduplicated sample of unresolved paths.
	//
	// Each entry is a contract-header declaration path that failed to
	// resolve.
	Paths []string
}

func (m *unresolvedDeclSample) Add(path string) {
	if len(m.Paths) >= unresolvedDeclPathSampleSize || !looksLikeContractHeaderPath(path) {
		return
	}

	if slices.Contains(m.Paths, path) {
		return
	}

	m.Paths = append(m.Paths, path)
}

func extractTypes(soPath, repoRoot string) ([]typeInfo, map[string]bool, []string, error) {
	f, err := elf.Open(soPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to open %s: %w", soPath, err)
	}

	defer f.Close()

	data, err := f.DWARF()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to read DWARF from %s: %w", soPath, err)
	}

	anonTypedefNames, err := collectAnonymousTypedefNames(data)
	if err != nil {
		return nil, nil, nil, err
	}

	byKey := map[string]typeInfo{}
	excluded := map[string]bool{}
	var unresolved unresolvedDeclSample
	reader := data.Reader()
	var lineFiles []*dwarf.LineFile
	var scheme bitfieldScheme
	for {
		entry, err := reader.Next()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to read DWARF entries: %w", err)
		}

		if entry == nil {
			break
		}

		if entry.Tag == dwarf.TagCompileUnit {
			lineFiles, err = compileUnitFiles(data, entry)
			if err != nil {
				return nil, nil, nil, err
			}

			scheme, err = compileUnitBitfieldScheme(data, entry)
			if err != nil {
				return nil, nil, nil, err
			}

			continue
		}

		var info typeInfo
		var ok bool
		var excludedKey string
		switch entry.Tag {
		case dwarf.TagStructType, dwarf.TagUnionType:
			info, ok, excludedKey, err = readTypeEntry(data, entry, lineFiles, scheme, repoRoot, &unresolved, anonTypedefNames)
		case dwarf.TagEnumerationType:
			info, ok, excludedKey, err = readEnumEntry(data, entry, lineFiles, repoRoot, &unresolved)
		default:
			continue
		}

		if err != nil {
			return nil, nil, nil, err
		}

		if excludedKey != "" {
			excluded[excludedKey] = true
		}

		if !ok {
			continue
		}

		key := info.Kind + " " + info.Name
		if existing, dup := byKey[key]; dup {
			if !sameType(existing, info) {
				return nil, nil, nil, fmt.Errorf(
					"conflicting definitions for %s %s (%s vs %s)",
					info.Kind, info.Name, existing.DeclPath, info.DeclPath,
				)
			}

			continue
		}

		byKey[key] = info
	}

	types := make([]typeInfo, 0, len(byKey))
	for _, info := range byKey {
		types = append(types, info)
	}

	return types, excluded, unresolved.Paths, nil
}

func compileUnitFiles(data *dwarf.Data, cu *dwarf.Entry) ([]*dwarf.LineFile, error) {
	lr, err := data.LineReader(cu)
	if err != nil {
		return nil, fmt.Errorf("failed to read line table for compile unit: %w", err)
	}

	if lr == nil {
		return nil, nil
	}

	return lr.Files(), nil
}

func compileUnitBitfieldScheme(data *dwarf.Data, cu *dwarf.Entry) (bitfieldScheme, error) {
	if !cu.Children {
		return bitfieldSchemeLegacy, nil
	}

	r := data.Reader()
	r.Seek(cu.Offset)
	if _, err := r.Next(); err != nil {
		return bitfieldSchemeLegacy, fmt.Errorf("failed to seek compile unit at offset %d: %w", cu.Offset, err)
	}

	depth := 0
	for {
		entry, err := r.Next()
		if err != nil {
			return bitfieldSchemeLegacy, fmt.Errorf("failed to scan compile unit for bit-field scheme: %w", err)
		}

		if entry == nil {
			break
		}

		if entry.Tag == 0 {
			if depth == 0 {
				break
			}

			depth--
			continue
		}

		if entry.Tag == dwarf.TagMember && entry.Val(dwarf.AttrBitSize) != nil {
			if entry.Val(dwarf.AttrDataBitOffset) != nil {
				return bitfieldSchemeDataBitOffset, nil
			}
		}

		if entry.Children {
			depth++
		}
	}

	return bitfieldSchemeLegacy, nil
}

func collectAnonymousTypedefNames(data *dwarf.Data) (map[*dwarf.StructType]string, error) {
	names := map[*dwarf.StructType]string{}
	reader := data.Reader()
	for {
		entry, err := reader.Next()
		if err != nil {
			return nil, fmt.Errorf("failed to scan DWARF entries for typedefs: %w", err)
		}

		if entry == nil {
			break
		}

		if entry.Tag != dwarf.TagTypedef {
			continue
		}

		name, _ := entry.Val(dwarf.AttrName).(string)
		if name == "" {
			continue
		}

		typ, err := data.Type(entry.Offset)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve typedef %s at offset %d: %w", name, entry.Offset, err)
		}

		td, tdOK := typ.(*dwarf.TypedefType)
		if !tdOK {
			continue
		}

		st := asStructType(td.Type)
		if st == nil || st.StructName != "" {
			continue
		}

		if _, exists := names[st]; !exists {
			names[st] = name
		}
	}

	return names, nil
}

func readTypeEntry(
	data *dwarf.Data, entry *dwarf.Entry, lineFiles []*dwarf.LineFile, scheme bitfieldScheme, repoRoot string,
	unresolved *unresolvedDeclSample, anonTypedefNames map[*dwarf.StructType]string,
) (info typeInfo, ok bool, excludedKey string, err error) {
	if entry.Val(dwarf.AttrDeclaration) != nil {
		return typeInfo{}, false, "", nil
	}

	declFile, declOK := entry.Val(dwarf.AttrDeclFile).(int64)
	if !declOK || declFile < 0 || int(declFile) >= len(lineFiles) || lineFiles[declFile] == nil {
		return typeInfo{}, false, "", nil
	}

	typ, err := data.Type(entry.Offset)
	if err != nil {
		return typeInfo{}, false, "", fmt.Errorf("failed to resolve type at offset %d: %w", entry.Offset, err)
	}

	st, stOK := typ.(*dwarf.StructType)
	if !stOK || st.Incomplete || (st.Kind != "struct" && st.Kind != "union") {
		return typeInfo{}, false, "", nil
	}

	name, _ := entry.Val(dwarf.AttrName).(string)
	key := st.Kind
	if name == "" {
		typedefName, viaTypedef := anonTypedefNames[st]
		if !viaTypedef {
			return typeInfo{}, false, "", nil
		}

		name = typedefName
		key = "typedef"
	}

	declPath, pathOK := typeDeclPath(lineFiles[declFile].Name, repoRoot)
	if !pathOK {
		unresolved.Add(lineFiles[declFile].Name)
		return typeInfo{}, false, key + " " + name, nil
	}

	members, err := flattenMembers(st, 0, scheme, "")
	if err != nil {
		return typeInfo{}, false, "", fmt.Errorf("failed to describe members of %s %s: %w", key, name, err)
	}

	alignment, _ := entry.Val(dwarf.AttrAlignment).(int64)
	info = typeInfo{Kind: key, Name: name, ByteSize: st.ByteSize, Alignment: alignment, DeclPath: declPath}
	info.Members = members
	return info, true, "", nil
}

func readEnumEntry(
	data *dwarf.Data, entry *dwarf.Entry, lineFiles []*dwarf.LineFile, repoRoot string,
	unresolved *unresolvedDeclSample,
) (info typeInfo, ok bool, excludedKey string, err error) {
	if entry.Val(dwarf.AttrDeclaration) != nil {
		return typeInfo{}, false, "", nil
	}

	declFile, declOK := entry.Val(dwarf.AttrDeclFile).(int64)
	if !declOK || declFile < 0 || int(declFile) >= len(lineFiles) || lineFiles[declFile] == nil {
		return typeInfo{}, false, "", nil
	}

	typ, err := data.Type(entry.Offset)
	if err != nil {
		return typeInfo{}, false, "", fmt.Errorf("failed to resolve enum type at offset %d: %w", entry.Offset, err)
	}

	et, etOK := typ.(*dwarf.EnumType)
	if !etOK || len(et.Val) == 0 {
		return typeInfo{}, false, "", nil
	}

	name, _ := entry.Val(dwarf.AttrName).(string)
	anonymous := name == ""

	declPath, pathOK := typeDeclPath(lineFiles[declFile].Name, repoRoot)
	if !pathOK {
		unresolved.Add(lineFiles[declFile].Name)
		if anonymous {
			return typeInfo{}, false, "", nil
		}

		return typeInfo{}, false, "enum " + name, nil
	}

	if anonymous {
		name = anonymousEnumName(et.Val)
	}

	info = typeInfo{Kind: "enum", Name: name, ByteSize: et.ByteSize, DeclPath: declPath}
	info.Enumerators = make([]enumConstant, len(et.Val))
	for idx, v := range et.Val {
		info.Enumerators[idx] = enumConstant{Name: v.Name, Value: v.Val}
	}

	return info, true, "", nil
}

func anonymousEnumName(values []*dwarf.EnumValue) string {
	names := make([]string, len(values))
	for idx, v := range values {
		names[idx] = v.Name
	}

	sort.Strings(names)
	return "anon@" + strings.Join(names, ",")
}

func flattenMembers(st *dwarf.StructType, baseByteOffset int64, scheme bitfieldScheme, namePrefix string) ([]typeMember, error) {
	var members []typeMember
	for _, field := range st.Field {
		nested := asStructType(field.Type)
		if nested != nil && nested.StructName == "" {
			nestedMembers, err := flattenMembers(
				nested, baseByteOffset+field.ByteOffset, scheme, joinMemberName(namePrefix, field.Name),
			)
			if err != nil {
				return nil, err
			}

			members = append(members, nestedMembers...)
			continue
		}

		desc, err := describeType(field.Type)
		if err != nil {
			return nil, err
		}

		member := typeMember{
			Name:       joinMemberName(namePrefix, field.Name),
			Type:       desc,
			ByteOffset: baseByteOffset + field.ByteOffset,
			BitSize:    field.BitSize,
		}

		if field.BitSize != 0 {
			member.DataBitOffset = baseByteOffset*8 + normalizedDataBitOffset(field, scheme)
		}

		members = append(members, member)
	}

	return members, nil
}

func joinMemberName(prefix, name string) string {
	switch {
	case prefix == "":
		return name
	case name == "":
		return prefix
	default:
		return prefix + "." + name
	}
}

func asStructType(t dwarf.Type) *dwarf.StructType {
	for {
		switch v := t.(type) {
		case *dwarf.StructType:
			return v
		case *dwarf.TypedefType:
			t = v.Type
		case *dwarf.QualType:
			t = v.Type
		default:
			return nil
		}
	}
}

func normalizedDataBitOffset(field *dwarf.StructField, scheme bitfieldScheme) int64 {
	if field.BitSize == 0 {
		return 0
	}

	if scheme == bitfieldSchemeDataBitOffset {
		return field.ByteOffset*8 + field.DataBitOffset
	}

	return field.ByteOffset*8 + field.ByteSize*8 - field.BitOffset - field.BitSize
}

var closureRootTypes = []string{
	"struct module",
	"struct dp_worker",
	"struct module_ectx",
	"struct packet_front",
	"struct packet",
	"struct cp_module",
	"struct dp_config",
	"struct cp_config",
	"struct counter_storage",
}

var requiredCoreTypes = append(append([]string{}, closureRootTypes...), "struct packet_list")

var requiredBoundaryEnums = []string{"enum packet_flag", "enum ip_family", "enum transport_proto"}

var coverageClosureRoots = append(append([]string{}, closureRootTypes...), requiredBoundaryEnums...)

func validateTypeCoverage(types []typeInfo, functions []functionInfo, excludedNames map[string]bool) error {
	if len(types) == 0 {
		return fmt.Errorf("extracted zero ABI-boundary types; decl_file resolution likely broke")
	}

	byKey := map[string]typeInfo{}
	for _, t := range types {
		byKey[t.Kind+" "+t.Name] = t
	}

	var missing []string
	for _, key := range requiredCoreTypes {
		root, ok := byKey[key]
		if !ok {
			missing = append(missing, key)
			continue
		}

		if root.ByteSize == 0 || len(root.Members) == 0 {
			missing = append(missing, key+" (empty)")
		}
	}

	for _, key := range requiredBoundaryEnums {
		root, ok := byKey[key]
		if !ok {
			missing = append(missing, key)
			continue
		}

		if root.ByteSize == 0 || len(root.Enumerators) == 0 {
			missing = append(missing, key+" (empty)")
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("manifest is missing required core ABI types: %s", strings.Join(missing, ", "))
	}

	_, byValue, byPointer := typeClosure(byKey, coverageClosureRoots, functions)
	var genuine []string
	for _, key := range byValue {
		if !excludedNames[key] {
			genuine = append(genuine, key)
		}
	}

	if len(genuine) > 0 {
		msg := fmt.Sprintf("manifest is missing the type closure of the ABI boundary by value: %s", strings.Join(genuine, ", "))
		if len(byPointer) > 0 {
			msg += fmt.Sprintf(" (also reached only by pointer, not required: %s)", strings.Join(byPointer, ", "))
		}

		return fmt.Errorf("%s", msg)
	}

	return nil
}

func typeClosure(
	byKey map[string]typeInfo, roots []string, functions []functionInfo,
) (visited map[string]bool, byValue, byPointer []string) {
	visited = map[string]bool{}
	missingByValue := map[string]bool{}
	missingByPointer := map[string]bool{}

	var walk func(key string)
	considerRef := func(refKind, refName string, viaPointer bool) {
		refKey := refKind + " " + refName
		if _, ok := byKey[refKey]; !ok {
			if viaPointer {
				missingByPointer[refKey] = true
			} else {
				missingByValue[refKey] = true
			}

			return
		}

		walk(refKey)
	}

	walk = func(key string) {
		if visited[key] {
			return
		}

		visited[key] = true
		t, ok := byKey[key]
		if !ok {
			return
		}

		for _, m := range t.Members {
			refKind, refName, viaPointer, ok := parseNamedTypeRef(m.Type)
			if !ok {
				continue
			}

			considerRef(refKind, refName, viaPointer)
		}
	}

	for _, root := range roots {
		walk(root)
	}

	for _, fn := range functions {
		for _, desc := range fnBoundaryTypeDescs(fn) {
			refKind, refName, viaPointer, ok := parseNamedTypeRef(desc)
			if !ok {
				continue
			}

			considerRef(refKind, refName, viaPointer)
		}
	}

	for key := range missingByValue {
		byValue = append(byValue, key)
	}

	for key := range missingByPointer {
		byPointer = append(byPointer, key)
	}

	sort.Strings(byValue)
	sort.Strings(byPointer)
	return visited, byValue, byPointer
}

func fnBoundaryTypeDescs(fn functionInfo) []string {
	descs := make([]string, 0, len(fn.Params)+1)
	descs = append(descs, fn.Return)
	return append(descs, fn.Params...)
}

func parseNamedTypeRef(desc string) (kind, name string, viaPointer, ok bool) {
	for {
		switch {
		case strings.HasPrefix(desc, "*"):
			viaPointer = true
			desc = desc[1:]
		case strings.HasPrefix(desc, "["):
			idx := strings.Index(desc, "]")
			if idx < 0 {
				return "", "", false, false
			}

			desc = desc[idx+1:]
		default:
			kind, name, ok = strings.Cut(desc, " ")
			if !ok || (kind != "struct" && kind != "union" && kind != "enum") {
				return "", "", false, false
			}

			return kind, name, viaPointer, true
		}
	}
}

func sameType(a, b typeInfo) bool {
	if a.Kind != b.Kind || a.Name != b.Name || a.ByteSize != b.ByteSize || a.Alignment != b.Alignment {
		return false
	}

	if len(a.Members) != len(b.Members) || len(a.Enumerators) != len(b.Enumerators) {
		return false
	}

	for idx := range a.Members {
		if a.Members[idx] != b.Members[idx] {
			return false
		}
	}

	for idx := range a.Enumerators {
		if a.Enumerators[idx] != b.Enumerators[idx] {
			return false
		}
	}

	return true
}
