// Command shmgen generates Go views of the C structures YANET keeps in
// shared memory, from the DWARF of a built binary.
//
// The layout is read from what the compiler emitted for the binary: each
// field of a view sits at the offset the C compiler chose, gaps become
// explicit padding, and every definition the binary carries of one
// structure must agree. A generated test pins each size and offset, so a
// view the Go compiler lays out differently fails before it touches
// shared memory. The build runs it on a binary compiled from the
// structures' headers as the dataplane compiles them (the meson target
// native-layout).
//
// The enumerators of the enumerations named as constants become typed
// unsigned Go constants as wide as their enumeration, so values the headers
// define come from the same compilation as the views.
//
// Usage:
//
//	shmgen -binary build/controlplane/native/layout/native-layout-types \
//		-package layout -import <package path> -out layout.go \
//		-test layout_test.go -local dp_worker.rx_count \
//		-relative module_ectx.abs_runtime_counter_storages \
//		-constants native_layout_u64 cp_config dp_config ...
package main

import (
	"bytes"
	"debug/dwarf"
	"debug/elf"
	"flag"
	"fmt"
	"go/format"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func main() {
	binary := flag.String("binary", "", "ELF file with DWARF")
	packageName := flag.String("package", "layout", "Go package name")
	importPath := flag.String("import", "", "import path of the package, for the test")
	output := flag.String("out", "", "Go file to write")
	testOutput := flag.String("test", "", "Go test file to write")
	locals := flag.String("local", "",
		"comma-separated struct.field pointers that hold a process-local address")
	relatives := flag.String("relative", "",
		"comma-separated struct.field pointers to pointers that C stores relative to absolute elements")
	constants := flag.String("constants", "",
		"comma-separated enums whose enumerators become typed unsigned constants")
	flag.Parse()

	if *binary == "" || *output == "" || (flag.NArg() == 0 && *constants == "") {
		fmt.Fprintln(os.Stderr, "usage: shmgen -binary FILE -out FILE [-test FILE -import PATH] "+
			"[-local STRUCT.FIELD,...] [-relative STRUCT.FIELD,...] [-constants ENUM,...] [STRUCT...]",
		)
		os.Exit(2)
	}
	options := options{
		Binary:      *binary,
		PackageName: *packageName,
		ImportPath:  *importPath,
		Output:      *output,
		TestOutput:  *testOutput,
		Locals:      map[string]bool{},
		Relatives:   map[string]bool{},
	}
	for local := range strings.SplitSeq(*locals, ",") {
		if local != "" {
			options.Locals[local] = true
		}
	}
	for relative := range strings.SplitSeq(*relatives, ",") {
		if relative != "" {
			options.Relatives[relative] = true
		}
	}
	for constant := range strings.SplitSeq(*constants, ",") {
		if constant != "" {
			options.Constants = append(options.Constants, constant)
		}
	}
	if err := run(options, flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "shmgen: %v\n", err)
		os.Exit(1)
	}
}

// options holds the command line.
type options struct {
	Binary      string
	PackageName string
	ImportPath  string
	Output      string
	TestOutput  string
	// Locals holds the struct.field pointers that hold an address only
	// the writing process can use.
	Locals map[string]bool
	// Relatives holds the struct.field pointers to pointers C stores
	// relative while their elements stay absolute.
	Relatives map[string]bool
	// Constants holds the enumerations whose enumerators become typed
	// constants.
	Constants []string
}

func run(options options, roots []string) error {
	binary, packageName, importPath := options.Binary, options.PackageName, options.ImportPath
	output, testOutput := options.Output, options.TestOutput
	if testOutput != "" && importPath == "" {
		return fmt.Errorf("-test needs -import")
	}
	file, err := elf.Open(binary)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := file.DWARF()
	if err != nil {
		return fmt.Errorf("read DWARF of %s: %w", binary, err)
	}
	// The layout depends on the target (cache-line size, alignment), so
	// views apply only to the architecture of the binary they come from.
	goarch, ok := map[elf.Machine]string{
		elf.EM_X86_64:  "amd64",
		elf.EM_AARCH64: "arm64",
	}[file.Machine]
	if !ok {
		return fmt.Errorf("%s: unsupported machine %v", binary, file.Machine)
	}
	constraint := "//go:build linux && " + goarch

	m, err := newGenerator(data, options.Locals, options.Relatives)
	if err != nil {
		return err
	}
	for _, root := range roots {
		if err := m.addRoot(root); err != nil {
			return err
		}
	}
	if err := m.drain(); err != nil {
		return err
	}
	if err := m.checkKeys(); err != nil {
		return err
	}
	for _, constant := range options.Constants {
		if err := m.addConstants(constant); err != nil {
			return err
		}
	}

	if err := m.checkNames(); err != nil {
		return err
	}

	// Both files are rendered before either is written, so a failure
	// never leaves new views beside an old test.
	source, err := m.render(packageName, filepath.Base(binary), constraint)
	if err != nil {
		return err
	}
	var test []byte
	if testOutput != "" {
		test, err = m.renderTest(packageName, importPath, filepath.Base(binary), constraint)
		if err != nil {
			return err
		}
	}
	if err := writeFile(output, source); err != nil {
		return err
	}
	if testOutput == "" {
		return nil
	}
	return writeFile(testOutput, test)
}

// checkNames fails on an enumerator constant that takes the name of a
// view; two Go declarations of one name would not compile.
func (m *generator) checkNames() error {
	var collisions []string
	for _, name := range sortedKeys(m.constants) {
		if _, ok := m.views[name]; ok {
			collisions = append(collisions, name)
		}
	}
	if len(collisions) > 0 {
		return fmt.Errorf("enumerator constants take view names: %s", strings.Join(collisions, ", "))
	}
	return nil
}

// writeFile replaces a file with data unless it holds data already, in one
// rename, so a Go build reading the file meanwhile sees it whole.
func writeFile(path string, data []byte) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	// A dot-prefixed temporary file is one the Go tools never read.
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

// check is one size or offset the generated test pins.
type check struct {
	Name       string
	Expression string
	Want       int64
}

// typedConstant is one enumerator generated as a typed unsigned constant.
type typedConstant struct {
	Name  string
	Type  string
	Value uint64
}

// view is the Go source of one generated type.
type view struct {
	Source string
}

type generator struct {
	data *dwarf.Data
	// definitions holds every complete definition of a named structure
	// or union, by tag name.
	definitions map[string][]dwarf.Offset
	// names maps a DWARF type to the Go type generated for it.
	names map[dwarf.Type]string
	// views holds the generated types by Go name.
	views map[string]*view
	// constants holds the enumerators, by Go name.
	constants map[string]string
	// typedConstants holds the enumerators of the enumerations named as
	// constants, in the order they were named.
	typedConstants []typedConstant
	// enums holds both definitions and forward declarations by tag name.
	enums map[string][]*dwarf.EnumType
	// signedEnums holds known signedness, including information from
	// compatible declarations in other units.
	signedEnums map[*dwarf.EnumType]bool
	// atomics maps each _Atomic type to the type it qualifies, which
	// debug/dwarf does not resolve.
	atomics map[dwarf.Type]dwarf.Type
	queue   []pending
	checks  []check
	// methods holds the accessors of flexible array members.
	methods []string
	// namedTypes holds the first DWARF type queued under each Go name.
	namedTypes map[string]dwarf.Type
	// conflicts collects Go names whose C types disagree.
	conflicts []string
	// locals holds the struct.field pointers that hold a process-local
	// address.
	locals map[string]bool
	// relatives holds the struct.field pointers to pointers C stores
	// relative while their elements stay absolute.
	relatives map[string]bool
	// matched holds the local and relative struct.field keys a view
	// field took.
	matched map[string]bool
}

// pending is a type waiting to be generated.
type pending struct {
	Name string
	Type dwarf.Type
}

func newGenerator(data *dwarf.Data, locals, relatives map[string]bool) (*generator, error) {
	m := &generator{
		data:        data,
		locals:      locals,
		relatives:   relatives,
		matched:     map[string]bool{},
		definitions: map[string][]dwarf.Offset{},
		names:       map[dwarf.Type]string{},
		views:       map[string]*view{},
		constants:   map[string]string{},
		atomics:     map[dwarf.Type]dwarf.Type{},
		namedTypes:  map[string]dwarf.Type{},

		enums:       map[string][]*dwarf.EnumType{},
		signedEnums: map[*dwarf.EnumType]bool{},
	}
	reader := data.Reader()
	for {
		entry, err := reader.Next()
		if err != nil {
			return nil, err
		}
		if entry == nil {
			break
		}
		if entry.Tag == dwarf.TagAtomicType {
			if err := m.addAtomic(entry); err != nil {
				return nil, err
			}
			continue
		}
		if entry.Tag == dwarf.TagEnumerationType {
			if err := m.addEnum(entry); err != nil {
				return nil, err
			}
			continue
		}
		if entry.Tag != dwarf.TagStructType && entry.Tag != dwarf.TagUnionType {
			continue
		}
		name, _ := entry.Val(dwarf.AttrName).(string)
		declaration, _ := entry.Val(dwarf.AttrDeclaration).(bool)
		if name == "" || declaration {
			continue
		}
		m.definitions[name] = append(m.definitions[name], entry.Offset)
	}
	return m, nil
}

// addAtomic records the type an _Atomic qualifier applies to.
//
// debug/dwarf caches types by offset, so a field typed by this qualifier
// refers to the same value as the key.
func (m *generator) addAtomic(entry *dwarf.Entry) error {
	underlying, ok := entry.Val(dwarf.AttrType).(dwarf.Offset)
	if !ok {
		return nil
	}
	atomicType, err := m.data.Type(entry.Offset)
	if err != nil {
		return err
	}
	typ, err := m.data.Type(underlying)
	if err != nil {
		return err
	}
	m.atomics[atomicType] = typ
	return nil
}

// addEnum indexes a named enumeration and records whether its underlying
// type is signed.
func (m *generator) addEnum(entry *dwarf.Entry) error {
	typ, err := m.data.Type(entry.Offset)
	if err != nil {
		return err
	}
	enumType, ok := typ.(*dwarf.EnumType)
	if !ok {
		return nil
	}
	// Some producers record an integer encoding on the enumeration itself.
	const (
		encodingSigned       = 0x05
		encodingSignedChar   = 0x06
		encodingUnsigned     = 0x07
		encodingUnsignedChar = 0x08
	)
	encoding, _ := entry.Val(dwarf.AttrEncoding).(int64)
	switch encoding {
	case encodingSigned, encodingSignedChar:
		m.signedEnums[enumType] = true
	case encodingUnsigned, encodingUnsignedChar:
		m.signedEnums[enumType] = false
	}
	if underlying, ok := entry.Val(dwarf.AttrType).(dwarf.Offset); ok {
		base, err := m.data.Type(underlying)
		if err != nil {
			return err
		}
		switch m.resolved(base).(type) {
		case *dwarf.IntType, *dwarf.CharType:
			m.signedEnums[enumType] = true
		case *dwarf.UintType, *dwarf.UcharType:
			m.signedEnums[enumType] = false
		}
	}
	if enumType.EnumName != "" {
		m.enums[enumType.EnumName] = append(m.enums[enumType.EnumName], enumType)
	}
	return nil
}

// addConstants generates an enumeration's enumerators as unsigned constants
// of its size.
//
// Every definition of the enumeration in the binary must agree. An
// enumerator's constant is named after it less the prefix it shares
// with its enumeration's name: in enum native_layout_u32, the enumerator
// native_layout_module_ABI_version becomes ModuleABIVersion.
func (m *generator) addConstants(name string) error {
	first, err := m.enumDefinition(name)
	if err != nil {
		return err
	}
	if first == nil || len(first.Val) == 0 {
		return fmt.Errorf("enum %s: no definition in the binary", name)
	}
	if m.enumSigned(first) {
		return fmt.Errorf("enum %s: signed, by its underlying type or a negative enumerator", name)
	}
	if !slices.Contains([]int64{1, 2, 4, 8}, first.ByteSize) {
		return fmt.Errorf("enum %s: %d bytes is no integer size", name, first.ByteSize)
	}
	goType := integer(first.ByteSize, false)
	for _, value := range first.Val {
		constant := camel(enumeratorStem(name, value.Name))
		if constant == "" {
			return fmt.Errorf("enum %s: enumerator %s names no constant", name, value.Name)
		}
		_, taken := m.constants[constant]
		_, isView := m.views[constant]
		if taken || isView || slices.ContainsFunc(m.typedConstants, func(other typedConstant) bool {
			return other.Name == constant
		}) {
			return fmt.Errorf("enum %s: enumerator %s: %s is taken", name, value.Name, constant)
		}
		unsigned, err := unsignedValue(value.Val, first.ByteSize)
		if err != nil {
			return fmt.Errorf("enum %s: enumerator %s: %w", name, value.Name, err)
		}
		m.typedConstants = append(m.typedConstants, typedConstant{Name: constant, Type: goType, Value: unsigned})
	}
	return nil
}

// enumDefinition reconciles the integer representation and enumerators
// recorded in different compilation units.
//
// A forward declaration can supply a width or signedness while omitting
// the enumerators. Only the properties present in both units must agree.
func (m *generator) enumDefinition(name string) (*dwarf.EnumType, error) {
	return m.reconcileEnums(name, m.enums[name])
}

func (m *generator) reconcileEnums(name string, entries []*dwarf.EnumType) (*dwarf.EnumType, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	label := "enum " + name
	if name == "" {
		label = "anonymous enum"
	}
	disagreement := func(first, other *dwarf.EnumType) error {
		return fmt.Errorf("%s: definitions disagree:\n%s\n%s", label, m.enumSignature(first), m.enumSignature(other))
	}
	var sized, signed, complete *dwarf.EnumType
	for _, entry := range entries {
		if entry.EnumName != entries[0].EnumName {
			return nil, disagreement(entries[0], entry)
		}
		if entry.ByteSize > 0 {
			if sized != nil && sized.ByteSize != entry.ByteSize {
				return nil, disagreement(sized, entry)
			}
			sized = entry
		}
		if sign, known := m.signedEnums[entry]; known {
			if signed != nil && m.signedEnums[signed] != sign {
				return nil, disagreement(signed, entry)
			}
			signed = entry
		}
		if len(entry.Val) != 0 {
			if complete != nil && complete.String() != entry.String() {
				return nil, disagreement(complete, entry)
			}
			complete = entry
		}
	}
	if sized == nil || (complete == nil && signed == nil) {
		return nil, fmt.Errorf("%s: incomplete integer representation in the binary", label)
	}
	if complete == nil {
		complete = sized
	}
	for _, entry := range entries {
		entry.ByteSize = sized.ByteSize
		if signed != nil {
			m.signedEnums[entry] = m.signedEnums[signed]
		}
	}
	return complete, nil
}

func (m *generator) enumSignature(typ *dwarf.EnumType) string {
	sign := "unknown signedness"
	if m.signedEnums[typ] {
		sign = "signed"
	} else if _, known := m.signedEnums[typ]; known {
		sign = "unsigned"
	}
	return fmt.Sprintf("size %d %s %s", typ.ByteSize, sign, typ)
}

// enumView selects the complete enumeration when another unit defines it.
func (m *generator) enumView(typ *dwarf.EnumType) *dwarf.EnumType {
	full, err := m.enumDefinition(typ.EnumName)
	if err != nil {
		m.conflicts = append(m.conflicts, err.Error())
	} else if full != nil {
		return full
	}
	return typ
}

// enumeratorStem returns an enumerator's name less the prefix, up to an
// underscore, it shares with its enumeration's name.
func enumeratorStem(enumName, enumerator string) string {
	prefix := enumName + "_"
	shared := 0
	for shared < len(prefix) && shared < len(enumerator) && prefix[shared] == enumerator[shared] {
		shared++
	}
	return enumerator[strings.LastIndexByte(enumerator[:shared], '_')+1:]
}

// unsignedValue returns an enumerator's value as an unsigned integer of
// size bytes.
//
// debug/dwarf reads every value as an int64: a 64-bit value with its top
// bit set comes back negative, and a narrower one may come back with its
// sign extended; both are the same bits.
func unsignedValue(value, size int64) (uint64, error) {
	bits := uint(size * 8)
	unsigned := uint64(value)
	if bits >= 64 {
		return unsigned, nil
	}
	if high := unsigned >> bits; high != 0 && high != 1<<(64-bits)-1 {
		return 0, fmt.Errorf("value %d does not fit %d bytes", value, size)
	}
	return unsigned & (1<<bits - 1), nil
}

// checkKeys fails on a local or relative struct.field key no view field took.
//
// A renamed field or structure would otherwise leave a pointer silently
// typed as a shared one.
func (m *generator) checkKeys() error {
	var unmatched []string
	for _, keys := range []struct {
		Flag string
		Keys map[string]bool
	}{{"-local", m.locals}, {"-relative", m.relatives}} {
		for _, key := range sortedKeys(keys.Keys) {
			if !m.matched[key] {
				unmatched = append(unmatched, keys.Flag+" "+key)
			}
		}
	}
	if len(unmatched) > 0 {
		return fmt.Errorf("keys match no field of the views:\n%s", strings.Join(unmatched, "\n"))
	}
	return nil
}

// addRoot queues the structure of that tag name, after checking that
// every definition of it in the binary has one layout.
func (m *generator) addRoot(name string) error {
	typ, err := m.definition(name)
	if err != nil {
		return err
	}
	if typ == nil {
		return fmt.Errorf("struct %s: no definition in the binary", name)
	}
	m.named(typ, camel(name))
	return nil
}

// definition returns the complete definition of a structure or union, or
// nil when the binary has none.
//
// A structure compiled into several units has one definition per unit;
// they must agree field by field, else two parts of the binary disagree
// about the bytes they share.
func (m *generator) definition(name string) (*dwarf.StructType, error) {
	offsets := m.definitions[name]
	if len(offsets) == 0 {
		return nil, nil
	}
	var first *dwarf.StructType
	for _, offset := range offsets {
		typ, err := m.data.Type(offset)
		if err != nil {
			return nil, err
		}
		structType, ok := typ.(*dwarf.StructType)
		if !ok {
			return nil, fmt.Errorf("%s at %#x is %T", name, offset, typ)
		}
		if first == nil {
			first = structType
		} else if err := m.compareStructs(name, first, structType); err != nil {
			return nil, err
		}
	}
	return first, nil
}

func (m *generator) compareStructs(name string, first, other *dwarf.StructType) error {
	var left, right []*dwarf.EnumType
	firstLayout := m.structSignature(first, &left)
	otherLayout := m.structSignature(other, &right)
	if firstLayout != otherLayout || len(left) != len(right) {
		return fmt.Errorf("struct %s: definitions disagree:\n%s\n%s", name, firstLayout, otherLayout)
	}
	for idx, enum := range left {
		if _, err := m.reconcileEnums(enum.EnumName, []*dwarf.EnumType{enum, right[idx]}); err != nil {
			return fmt.Errorf("struct %s: definitions disagree: %w", name, err)
		}
	}
	return nil
}

func (m *generator) structSignature(typ *dwarf.StructType, enums *[]*dwarf.EnumType) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s %s size %d:", typ.Kind, typ.StructName, typ.ByteSize)
	for _, field := range typ.Field {
		fmt.Fprintf(&builder, " %s@%d/%d+%d:%s",
			field.Name, field.ByteOffset, field.DataBitOffset, field.BitSize, m.fieldSignature(field.Type, enums),
		)
	}
	return builder.String()
}

// fieldSignature records enum leaves in field order so their DWARF
// properties can be reconciled after the enclosing shapes match.
func (m *generator) fieldSignature(typ dwarf.Type, enums *[]*dwarf.EnumType) string {
	var prefix strings.Builder
	sawTypedef := false
	for current := typ; current != nil; {
		if atomic, ok := m.atomics[current]; ok {
			prefix.WriteString("_Atomic ")
			current = atomic
			continue
		}
		switch field := current.(type) {
		case *dwarf.QualType:
			prefix.WriteString(field.Qual + " ")
			current = field.Type
		case *dwarf.TypedefType:
			if !sawTypedef {
				prefix.WriteString(field.Name + " ")
				sawTypedef = true
			}
			current = field.Type
		case *dwarf.PtrType:
			prefix.WriteByte('*')
			current = field.Type
		case *dwarf.ArrayType:
			fmt.Fprintf(&prefix, "[%d]", field.Count)
			current = field.Type
		case *dwarf.EnumType:
			if field.EnumName != "" {
				field = m.enumView(field)
			}
			*enums = append(*enums, field)
			return prefix.String() + "enum " + field.EnumName
		case *dwarf.StructType:
			if field.StructName == "" {
				return prefix.String() + m.structSignature(field, enums)
			}
			return typ.String()
		default:
			return fmt.Sprintf("%s %s%T/%d", typ, prefix.String(), field, field.Size())
		}
	}
	return typ.String()
}

func (m *generator) drain() error {
	var enums []pending
	for len(m.queue) > 0 {
		next := m.queue[0]
		m.queue = m.queue[1:]
		var err error
		switch typ := next.Type.(type) {
		case *dwarf.StructType:
			err = m.emitStruct(next.Name, typ)
		case *dwarf.EnumType:
			enums = append(enums, next)
		}
		if err != nil {
			return err
		}
	}
	if len(m.conflicts) > 0 {
		slices.Sort(m.conflicts)
		return fmt.Errorf("types disagree between compilation units:\n%s", strings.Join(m.conflicts, "\n"))
	}
	for _, enum := range enums {
		if err := m.emitEnum(enum.Name, enum.Type.(*dwarf.EnumType)); err != nil {
			return err
		}
	}
	return nil
}

// goType returns the Go type a field of that C type is generated as.
//
// It queues the structures and enumerations the type names; context names
// an anonymous structure after the field that holds it.
func (m *generator) goType(typ dwarf.Type, context string) string {
	if atomic, ok := m.atomics[typ]; ok {
		return m.goType(atomic, context)
	}
	switch typ := typ.(type) {
	case *dwarf.QualType:
		return m.goType(typ.Type, context)
	case *dwarf.TypedefType:
		if structType, ok := m.underlying(typ.Type).(*dwarf.StructType); ok && structType.StructName == "" {
			return m.named(structType, camel(typ.Name))
		}
		if enumType, ok := m.underlying(typ.Type).(*dwarf.EnumType); ok && enumType.EnumName == "" {
			return m.named(enumType, camel(typ.Name))
		}
		return m.goType(typ.Type, context)
	case *dwarf.StructType:
		if typ.StructName == "" {
			return m.named(typ, context)
		}
		if existing, ok := m.names[typ]; ok {
			return existing
		}
		full, err := m.definition(typ.StructName)
		if err != nil {
			m.conflicts = append(m.conflicts, err.Error())
			return "byte"
		}
		if full == nil {
			// An opaque structure: nothing reads it through a view.
			return "byte"
		}
		return m.named(full, camel(typ.StructName))
	case *dwarf.EnumType:
		if typ.EnumName == "" {
			return integer(typ.ByteSize, m.enumSigned(typ))
		}
		return m.named(m.enumView(typ), camel(typ.EnumName))
	case *dwarf.BoolType:
		return "bool"
	case *dwarf.CharType:
		// Plain char shares the signed encoding where it is signed and
		// stays a byte; only an explicitly signed one is an int8.
		if typ.Name == "signed char" {
			return "int8"
		}
		return "byte"
	case *dwarf.UcharType:
		return "byte"
	case *dwarf.IntType, *dwarf.UintType, *dwarf.FloatType:
		if rawScalar(typ) {
			return fmt.Sprintf("[%d]byte", typ.Size())
		}
		if _, ok := typ.(*dwarf.FloatType); ok {
			return fmt.Sprintf("float%d", typ.Size()*8)
		}
		_, signed := typ.(*dwarf.IntType)
		return integer(typ.Size(), signed)
	case *dwarf.PtrType:
		switch m.resolved(typ.Type).(type) {
		case *dwarf.FuncType:
			return "shm.Local"
		case *dwarf.VoidType, nil:
			return "shm.Ptr[byte]"
		default:
			return "shm.Ptr[" + m.goType(typ.Type, context+"Target") + "]"
		}
	case *dwarf.ArrayType:
		if typ.Count < 0 {
			return "[0]" + m.goType(typ.Type, context+"Element")
		}
		return fmt.Sprintf("[%d]%s", typ.Count, m.goType(typ.Type, context+"Element"))
	case *dwarf.VoidType:
		return "byte"
	default:
		return fmt.Sprintf("[%d]byte", m.size(typ))
	}
}

func (m *generator) named(typ dwarf.Type, name string) string {
	if existing, ok := m.names[typ]; ok {
		return existing
	}
	m.names[typ] = name
	if previous, ok := m.namedTypes[name]; ok {
		if err := m.compareViews(name, previous, typ); err != nil {
			m.conflicts = append(m.conflicts, err.Error())
		}
		return name
	}
	m.namedTypes[name] = typ
	m.queue = append(m.queue, pending{Name: name, Type: typ})
	return name
}

func (m *generator) compareViews(name string, first, other dwarf.Type) error {
	switch first := first.(type) {
	case *dwarf.StructType:
		if current, ok := other.(*dwarf.StructType); ok {
			return m.compareStructs(name, first, current)
		}
	case *dwarf.EnumType:
		if current, ok := other.(*dwarf.EnumType); ok {
			_, err := m.reconcileEnums(name, []*dwarf.EnumType{first, current})
			return err
		}
	}
	return fmt.Errorf("%s names both a structure and an enumeration", name)
}

// fieldType returns the Go type of a structure field, applying what the C
// declarations do not say about pointers.
//
// A process-local pointer, an abs_ one or one named local, is an opaque
// word and must be an 8-byte pointer. A relative one, whatever its name,
// is a pointer to 8-byte pointers C stores relative while its elements
// stay absolute: a relative pointer to opaque words.
func (m *generator) fieldType(typ *dwarf.StructType, field *dwarf.StructField, context string, local, relative bool) (string, error) {
	if !local && !relative && !strings.HasPrefix(field.Name, "abs_") {
		return m.goType(field.Type, context), nil
	}
	pointer, ok := m.resolved(field.Type).(*dwarf.PtrType)
	if !ok || m.size(field.Type) != 8 {
		return "", fmt.Errorf(
			"struct %s: field %s is local or relative but no 8-byte pointer",
			typ.StructName, field.Name,
		)
	}
	if !relative {
		return "shm.Local", nil
	}
	if !m.isPointer(pointer.Type) {
		return "", fmt.Errorf(
			"struct %s: field %s is relative but no pointer to 8-byte pointers",
			typ.StructName, field.Name,
		)
	}
	return "shm.Ptr[shm.Local]", nil
}

// isPointer reports whether a C type is an 8-byte pointer.
func (m *generator) isPointer(typ dwarf.Type) bool {
	_, ok := m.resolved(typ).(*dwarf.PtrType)
	return ok && m.size(typ) == 8
}

// underlying strips qualifiers, _Atomic included: they change how C
// accesses a value, not its layout.
func (m *generator) underlying(typ dwarf.Type) dwarf.Type {
	for {
		if qualified, ok := typ.(*dwarf.QualType); ok {
			typ = qualified.Type
			continue
		}
		if atomic, ok := m.atomics[typ]; ok {
			typ = atomic
			continue
		}
		return typ
	}
}

// resolved strips qualifiers and typedefs.
func (m *generator) resolved(typ dwarf.Type) dwarf.Type {
	for {
		typ = m.underlying(typ)
		typedef, ok := typ.(*dwarf.TypedefType)
		if !ok {
			return typ
		}
		typ = typedef.Type
	}
}

// isAtomic reports whether C reads and writes the type atomically.
func (m *generator) isAtomic(typ dwarf.Type) bool {
	for {
		if _, ok := m.atomics[typ]; ok {
			return true
		}
		switch next := typ.(type) {
		case *dwarf.QualType:
			typ = next.Type
		case *dwarf.TypedefType:
			typ = next.Type
		default:
			return false
		}
	}
}

// size returns the size of a C type, which debug/dwarf leaves unknown
// for _Atomic types and the arrays and typedefs built on them.
func (m *generator) size(typ dwarf.Type) int64 {
	switch typ := m.resolved(typ).(type) {
	case *dwarf.EnumType:
		if typ.ByteSize <= 0 && typ.EnumName != "" {
			return m.enumView(typ).ByteSize
		}
		return typ.ByteSize
	case *dwarf.ArrayType:
		if typ.Count < 0 {
			return 0
		}
		return typ.Count * m.size(typ.Type)
	default:
		return typ.Size()
	}
}

// flexibleArray returns the array for a member omitted from the Go view.
func (m *generator) flexibleArray(typ dwarf.Type) *dwarf.ArrayType {
	if array, ok := m.resolved(typ).(*dwarf.ArrayType); ok && m.size(array) == 0 {
		return array
	}
	return nil
}

func integer(size int64, signed bool) string {
	name := fmt.Sprintf("int%d", size*8)
	if !signed {
		name = "u" + name
	}
	return name
}

// goAlign returns the alignment Go gives the view generated for a C type.
//
// It is at most a word, one for a union kept as raw bytes, and one for a
// structure member that C packed off its natural alignment.
func (m *generator) goAlign(typ dwarf.Type) int64 {
	switch typ := m.resolved(typ).(type) {
	case *dwarf.StructType:
		if typ.Kind == "union" {
			return 1
		}
		align := int64(1)
		for _, field := range typ.Field {
			if field.BitSize != 0 || m.flexibleArray(field.Type) != nil {
				continue
			}
			fieldAlign := m.goAlign(field.Type)
			if field.ByteOffset%fieldAlign != 0 {
				fieldAlign = 1
			}
			align = max(align, fieldAlign)
		}
		return align
	case *dwarf.ArrayType:
		return m.goAlign(typ.Type)
	case *dwarf.PtrType:
		return 8
	case *dwarf.IntType, *dwarf.UintType, *dwarf.FloatType, *dwarf.EnumType:
		if size := m.size(typ); size > 0 && !rawScalar(typ) {
			return min(size, 8)
		}
	}
	return 1
}

// rawScalar reports whether a C scalar has no Go type of its size, as
// __int128, long double or _Float16, and is kept as raw bytes.
func rawScalar(typ dwarf.Type) bool {
	switch typ := typ.(type) {
	case *dwarf.IntType, *dwarf.UintType:
		return !slices.Contains([]int64{1, 2, 4, 8}, typ.Size())
	case *dwarf.FloatType:
		return typ.ByteSize != 4 && typ.ByteSize != 8
	default:
		return false
	}
}

// enumSigned uses the DWARF encoding or underlying integer type when
// known and otherwise infers signedness from a negative enumerator.
func (m *generator) enumSigned(typ *dwarf.EnumType) bool {
	if signed, known := m.signedEnums[typ]; known {
		return signed
	}
	return slices.ContainsFunc(typ.Val, func(value *dwarf.EnumValue) bool {
		return value.Val < 0
	})
}

func (m *generator) emitStruct(name string, typ *dwarf.StructType) error {
	var body strings.Builder
	if typ.Kind == "union" {
		fmt.Fprintf(&body, "// %s is the C union %s; read its members through unsafe.\n",
			name, cName(typ),
		)
		fmt.Fprintf(&body, "type %s struct {\n\traw [%d]byte\n}\n", name, typ.ByteSize)
		m.views[name] = &view{Source: body.String()}
		m.checks = append(m.checks, check{
			Name: name + " size", Expression: fmt.Sprintf("unsafe.Sizeof(layout.%s{})", name), Want: typ.ByteSize,
		})
		return nil
	}

	fmt.Fprintf(&body, "// %s is the C struct %s, %d bytes.\n", name, cName(typ), typ.ByteSize)
	fmt.Fprintf(&body, "type %s struct {\n", name)
	cursor := int64(0)
	used := map[string]bool{}
	var accessors []string
	padding := 0
	pad := func(to int64) {
		if to > cursor {
			fmt.Fprintf(&body, "\t_ [%d]byte\n", to-cursor)
			padding++
			cursor = to
		}
	}

	fields := typ.Field
	for idx := 0; idx < len(fields); idx++ {
		field := fields[idx]
		if field.BitSize != 0 {
			// Bit-fields: keep the bytes they occupy as raw storage.
			first := field.DataBitOffset
			if first == 0 && field.ByteOffset != 0 {
				first = field.ByteOffset * 8
			}
			last := first + field.BitSize
			names := []string{field.Name}
			for idx+1 < len(fields) && fields[idx+1].BitSize != 0 {
				idx++
				next := fields[idx]
				end := next.DataBitOffset + next.BitSize
				if end > last {
					last = end
				}
				names = append(names, next.Name)
			}
			start := max(first/8, cursor)
			pad(start)
			size := (last+7)/8 - start
			fieldName := unique(used, fmt.Sprintf("Bits%d", start))
			fmt.Fprintf(&body, "\t%s [%d]byte // bit-fields %s\n", fieldName, size, strings.Join(names, ", "))
			cursor = start + size
			continue
		}

		key := typ.StructName + "." + field.Name
		local, relative := m.locals[key], m.relatives[key]
		if local && relative {
			return fmt.Errorf("struct %s: field %s is both local and relative", typ.StructName, field.Name)
		}
		if local || relative {
			m.matched[key] = true
		}

		offset := field.ByteOffset
		if array := m.flexibleArray(field.Type); array != nil {
			// A flexible array member: no bytes of its own, reached
			// through an accessor sized by the caller.
			//
			// One named local holds the absolute pointers only the
			// dataplane fills and follows, so it gets no accessor.
			if relative {
				return fmt.Errorf(
					"struct %s: field %s is a flexible array but -relative requires a pointer to 8-byte pointers",
					typ.StructName, field.Name,
				)
			}
			if local {
				if !m.isPointer(array.Type) {
					return fmt.Errorf(
						"struct %s: field %s is local but no flexible array of 8-byte pointers",
						typ.StructName, field.Name,
					)
				}
				fmt.Fprintf(&body, "\t// %s: dataplane-owned flexible array at offset %d.\n", field.Name, offset)
				continue
			}
			fmt.Fprintf(&body, "\t// %s: flexible array at offset %d.\n", field.Name, offset)
			element := m.goType(array.Type, name+camel(field.Name)+"Element")
			accessor := camel(field.Name)
			accessors = append(accessors, accessor)
			m.methods = append(m.methods, fmt.Sprintf(
				"// %[1]s returns the first count elements of the flexible array %[4]s.\n"+
					"func (m *%[2]s) %[1]s(count int) []%[3]s {\n"+
					"\treturn unsafe.Slice((*%[3]s)(unsafe.Add(unsafe.Pointer(m), %[5]d)), count)\n}\n",
				accessor, name, element, field.Name, offset,
			))
			// The accessor reads one element past the view.
			//
			// The buffer holds the view and that element, so the address
			// check stays inside one allocation.
			m.checks = append(m.checks, check{
				Name: name + "." + accessor + " address",
				Expression: fmt.Sprintf("func() uintptr {\n"+
					"\tvar probe *layout.%[1]s\n"+
					"\tbuffer := make([]uint64, (unsafe.Sizeof(layout.%[1]s{})+unsafe.Sizeof(probe.%[2]s(0)[0]))/8+1)\n"+
					"\tview := (*layout.%[1]s)(unsafe.Pointer(&buffer[0]))\n"+
					"\treturn uintptr(unsafe.Pointer(&view.%[2]s(1)[0])) - uintptr(unsafe.Pointer(view))\n"+
					"}()", name, accessor),
				Want: offset,
			})
			continue
		}
		if offset < cursor {
			return fmt.Errorf("struct %s: field %s at %d overlaps the previous one ending at %d",
				name, field.Name, offset, cursor,
			)
		}
		fieldName := camel(field.Name)
		if fieldName == "" {
			fieldName = fmt.Sprintf("Anonymous%d", offset)
		}
		fieldName = unique(used, fieldName)
		goType, err := m.fieldType(typ, field, name+fieldName, local, relative)
		if err != nil {
			return err
		}
		pad(offset)
		comment := ""
		if m.isAtomic(field.Type) {
			comment = " // atomic"
		}
		size := m.size(field.Type)
		if size == 0 && offset > 0 && offset == typ.ByteSize {
			return fmt.Errorf("struct %s: trailing zero-sized field %s would add Go padding", cName(typ), fieldName)
		}
		if offset%m.goAlign(field.Type) != 0 {
			fmt.Fprintf(&body, "\t%s [%d]byte // packed %s %s\n", fieldName, size, field.Name, goType)
		} else {
			fmt.Fprintf(&body, "\t%s %s%s\n", fieldName, goType, comment)
		}
		m.checks = append(m.checks, check{
			Name:       name + "." + fieldName + " offset",
			Expression: fmt.Sprintf("unsafe.Offsetof(layout.%s{}.%s)", name, fieldName),
			Want:       offset,
		})
		cursor = offset + size
	}
	if align := m.goAlign(typ); typ.ByteSize%align != 0 {
		return fmt.Errorf("struct %s: size %d is not a multiple of Go alignment %d", cName(typ), typ.ByteSize, align)
	}
	pad(typ.ByteSize)
	body.WriteString("}\n")
	for _, accessor := range accessors {
		if used[accessor] {
			return fmt.Errorf("struct %s: the accessor of a flexible array takes the field name %s", name, accessor)
		}
		used[accessor] = true
	}

	m.views[name] = &view{Source: body.String()}
	m.checks = append(m.checks, check{
		Name: name + " size", Expression: fmt.Sprintf("unsafe.Sizeof(layout.%s{})", name), Want: typ.ByteSize,
	})
	return nil
}

func (m *generator) emitEnum(name string, typ *dwarf.EnumType) error {
	signed := m.enumSigned(typ)
	var body strings.Builder
	fmt.Fprintf(&body, "// %s is the C enum %s.\n", name, typ.EnumName)
	fmt.Fprintf(&body, "type %s %s\n", name, integer(typ.ByteSize, signed))
	m.views[name] = &view{Source: body.String()}
	for _, value := range typ.Val {
		constant := camel(strings.ToLower(value.Name))
		if _, taken := m.constants[constant]; taken {
			return fmt.Errorf("enum %s: enumerator %s: constant %s is taken", typ.EnumName, value.Name, constant)
		}
		literal := strconv.FormatInt(value.Val, 10)
		if !signed {
			unsigned, err := unsignedValue(value.Val, typ.ByteSize)
			if err != nil {
				return fmt.Errorf("enum %s: enumerator %s: %w", typ.EnumName, value.Name, err)
			}
			literal = strconv.FormatUint(unsigned, 10)
		}
		m.constants[constant] = fmt.Sprintf("%s %s = %s", constant, name, literal)
	}
	return nil
}

func (m *generator) render(packageName, binary, constraint string) ([]byte, error) {
	var out bytes.Buffer
	fmt.Fprintf(&out, "// Code generated by shmgen from the DWARF of %s. DO NOT EDIT.\n\n", binary)
	fmt.Fprintf(&out, "%s\n\n", constraint)
	fmt.Fprintf(&out, "package %s\n\n", packageName)
	out.WriteString("import (\n\t\"unsafe\"\n\n\t\"github.com/yanet-platform/yanet2/controlplane/native/shm\"\n)\n\n")
	if len(m.constants) > 0 {
		names := sortedKeys(m.constants)
		out.WriteString("const (\n")
		for _, name := range names {
			fmt.Fprintf(&out, "\t%s\n", m.constants[name])
		}
		out.WriteString(")\n\n")
	}
	if len(m.typedConstants) > 0 {
		out.WriteString("// Values the headers define, from the same compilation as the views.\n")
		out.WriteString("const (\n")
		for _, constant := range m.typedConstants {
			fmt.Fprintf(&out, "\t%s %s = %#x // %d\n", constant.Name, constant.Type, constant.Value, constant.Value)
		}
		out.WriteString(")\n\n")
	}
	for _, name := range sortedKeys(m.views) {
		out.WriteString(m.views[name].Source)
		out.WriteString("\n")
	}
	slices.Sort(m.methods)
	for _, method := range m.methods {
		out.WriteString(method)
		out.WriteString("\n")
	}
	// Keep the imports used whatever the views hold.
	out.WriteString("var (\n\t_ shm.Local\n\t_ = unsafe.Pointer(nil)\n)\n")
	return format.Source(out.Bytes())
}

func (m *generator) renderTest(packageName, importPath, binary, constraint string) ([]byte, error) {
	var out bytes.Buffer
	fmt.Fprintf(&out, "// Code generated by shmgen from the DWARF of %s. DO NOT EDIT.\n\n", binary)
	fmt.Fprintf(&out, "%s\n\n", constraint)
	fmt.Fprintf(&out, "package %s_test\n\n", packageName)
	if len(m.checks) == 0 && len(m.typedConstants) == 0 {
		return format.Source(out.Bytes())
	}
	unsafeImport := ""
	if len(m.checks) > 0 {
		unsafeImport = "\t\"unsafe\"\n"
	}
	layoutImport := strconv.Quote(importPath)
	if packageName != "layout" {
		layoutImport = "layout " + layoutImport
	}
	fmt.Fprintf(&out, "import (\n\t\"testing\"\n%s\n\t\"github.com/stretchr/testify/require\"\n\n\t%s\n)\n\n",
		unsafeImport, layoutImport,
	)
	if len(m.checks) > 0 {
		out.WriteString("// Test_Layout_MatchesDWARF verifies that every view has the size and\n")
		out.WriteString("// field offsets the C compiler gave its structure.\n")
		out.WriteString("func Test_Layout_MatchesDWARF(t *testing.T) {\n")
		out.WriteString("\tcases := []struct {\n\t\tname string\n\t\tgot  uintptr\n\t\twant uintptr\n\t}{\n")
		for _, check := range m.checks {
			fmt.Fprintf(&out, "\t\t{%q, %s, %d},\n", check.Name, check.Expression, check.Want)
		}
		out.WriteString("\t}\n\tfor _, tc := range cases {\n")
		out.WriteString("\t\tt.Run(tc.name, func(t *testing.T) {\n\t\t\trequire.Equal(t, tc.want, tc.got)\n\t\t})\n\t}\n}\n")
	}
	if len(m.typedConstants) > 0 {
		out.WriteString("\n// Test_Layout_ConstantsMatchDWARF verifies that every constant has the\n")
		out.WriteString("// type and the value its enumerator has in the DWARF.\n")
		out.WriteString("func Test_Layout_ConstantsMatchDWARF(t *testing.T) {\n")
		out.WriteString("\tcases := []struct {\n\t\tname string\n\t\tgot  any\n\t\twant any\n\t}{\n")
		for _, constant := range m.typedConstants {
			fmt.Fprintf(&out, "\t\t{%q, layout.%s, %s(%#x)},\n", constant.Name, constant.Name, constant.Type, constant.Value)
		}
		out.WriteString("\t}\n\tfor _, tc := range cases {\n")
		out.WriteString("\t\tt.Run(tc.name, func(t *testing.T) {\n\t\t\trequire.Equal(t, tc.want, tc.got)\n\t\t})\n\t}\n}\n")
	}
	return format.Source(out.Bytes())
}

func sortedKeys[V any](values map[string]V) []string {
	return slices.Sorted(maps.Keys(values))
}

func unique(used map[string]bool, name string) string {
	candidate := name
	for idx := 2; used[candidate]; idx++ {
		candidate = fmt.Sprintf("%s%d", name, idx)
	}
	used[candidate] = true
	return candidate
}

func cName(typ *dwarf.StructType) string {
	if typ.StructName == "" {
		return "(anonymous)"
	}
	return typ.StructName
}

// camel turns a C identifier into an exported Go one: cp_config becomes
// CpConfig.
func camel(name string) string {
	var builder strings.Builder
	for part := range strings.SplitSeq(name, "_") {
		if part == "" {
			continue
		}
		builder.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	result := builder.String()
	if result != "" && result[0] >= '0' && result[0] <= '9' {
		result = "X" + result
	}
	return result
}
