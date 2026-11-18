// Command shmgen generates Go views of the C structures YANET keeps in
// shared memory, from the DWARF of a built binary.
//
// The layout is read from what the compiler emitted for the binary that
// runs: each field of a view sits at the offset the C compiler chose, gaps
// become explicit padding, and every definition the binary carries of one
// structure must agree. A generated test pins each size and offset, so a
// view the Go compiler lays out differently fails before it touches
// shared memory.
//
// Usage:
//
//	shmgen -binary build/dataplane/yanet-dataplane -package layout \
//		-import <package path> -out layout.go -test layout_test.go \
//		cp_config dp_config ...
package main

import (
	"bytes"
	"crypto/sha256"
	"debug/dwarf"
	"debug/elf"
	"encoding/hex"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
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
		"comma-separated abs_ struct.field pointers that C stores relative nonetheless")
	flag.Parse()

	if *binary == "" || *output == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: shmgen -binary FILE -out FILE [-test FILE -import PATH] STRUCT...")
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
	for _, local := range strings.Split(*locals, ",") {
		if local != "" {
			options.Locals[local] = true
		}
	}
	for _, relative := range strings.Split(*relatives, ",") {
		if relative != "" {
			options.Relatives[relative] = true
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
	// Relatives holds the abs_ struct.field pointers C stores relative.
	Relatives map[string]bool
}

func run(options options, roots []string) error {
	binary, packageName, importPath := options.Binary, options.PackageName, options.ImportPath
	output, testOutput := options.Output, options.TestOutput
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

	source, err := m.render(packageName, filepath.Base(binary), constraint)
	if err != nil {
		return err
	}
	if err := os.WriteFile(output, source, 0o644); err != nil {
		return err
	}
	if testOutput == "" {
		return nil
	}
	if importPath == "" {
		return fmt.Errorf("-test needs -import")
	}
	test, err := m.renderTest(packageName, importPath, filepath.Base(binary), constraint)
	if err != nil {
		return err
	}
	return os.WriteFile(testOutput, test, 0o644)
}

// check is one size or offset the generated test pins.
type check struct {
	Name       string
	Expression string
	Want       int64
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
	// atomics maps each _Atomic type to the type it qualifies, which
	// debug/dwarf does not resolve.
	atomics map[dwarf.Type]dwarf.Type
	queue   []pending
	checks  []check
	// fingerprint accumulates the layout of every view.
	fingerprint strings.Builder
	// methods holds the accessors of flexible array members.
	methods []string
	// signatures holds the layout each Go name was generated from: two
	// compilation units give one C type two DWARF values, which must
	// agree.
	signatures map[string]string
	// conflicts collects Go names whose C types disagree.
	conflicts []string
	// locals holds the struct.field pointers that hold a process-local
	// address.
	locals map[string]bool
	// relatives holds the abs_ struct.field pointers C stores relative.
	relatives map[string]bool
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
		definitions: map[string][]dwarf.Offset{},
		names:       map[dwarf.Type]string{},
		views:       map[string]*view{},
		constants:   map[string]string{},
		atomics:     map[dwarf.Type]dwarf.Type{},
		signatures:  map[string]string{},
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

// addAtomic records the type an _Atomic qualifier applies to. debug/dwarf
// caches types by offset, so a field typed by this qualifier refers to
// the same value as the key.
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
	m.goType(typ, "")
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
	var firstSignature string
	for _, offset := range offsets {
		typ, err := m.data.Type(offset)
		if err != nil {
			return nil, err
		}
		structType, ok := typ.(*dwarf.StructType)
		if !ok {
			return nil, fmt.Errorf("%s at %#x is %T", name, offset, typ)
		}
		signature := structSignature(structType)
		if first == nil {
			first, firstSignature = structType, signature
			continue
		}
		if signature != firstSignature {
			return nil, fmt.Errorf(
				"struct %s: definitions disagree:\n%s\n%s",
				name, firstSignature, signature,
			)
		}
	}
	return first, nil
}

func structSignature(typ *dwarf.StructType) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s %s size %d:", typ.Kind, typ.StructName, typ.ByteSize)
	for _, field := range typ.Field {
		fmt.Fprintf(&builder, " %s@%d/%d+%d:%s",
			field.Name, field.ByteOffset, field.DataBitOffset, field.BitSize, field.Type,
		)
	}
	return builder.String()
}

func (m *generator) drain() error {
	for len(m.queue) > 0 {
		next := m.queue[0]
		m.queue = m.queue[1:]
		var err error
		switch typ := next.Type.(type) {
		case *dwarf.StructType:
			err = m.emitStruct(next.Name, typ)
		case *dwarf.EnumType:
			m.emitEnum(next.Name, typ)
		}
		if err != nil {
			return err
		}
	}
	if len(m.conflicts) > 0 {
		sort.Strings(m.conflicts)
		return fmt.Errorf("types disagree between compilation units:\n%s", strings.Join(m.conflicts, "\n"))
	}
	return nil
}

// goType returns the Go type a field of that C type is generated as,
// queueing the structures and enumerations it names. context names an
// anonymous structure after the field that holds it.
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
		if typ.Incomplete && typ.StructName != "" {
			full, err := m.definition(typ.StructName)
			if err == nil && full != nil {
				return m.goType(full, context)
			}
			return "byte"
		}
		name := camel(typ.StructName)
		if typ.StructName == "" {
			name = context
		}
		return m.named(typ, name)
	case *dwarf.EnumType:
		if typ.EnumName == "" {
			return integer(typ.ByteSize, true)
		}
		return m.named(typ, camel(typ.EnumName))
	case *dwarf.BoolType:
		return "bool"
	case *dwarf.CharType, *dwarf.UcharType:
		return "byte"
	case *dwarf.IntType:
		return integer(typ.ByteSize, true)
	case *dwarf.UintType:
		return integer(typ.ByteSize, false)
	case *dwarf.FloatType:
		return fmt.Sprintf("float%d", typ.ByteSize*8)
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
	signature := typ.String()
	if structType, ok := typ.(*dwarf.StructType); ok {
		signature = structSignature(structType)
	}
	if previous, ok := m.signatures[name]; ok {
		if previous != signature {
			m.conflicts = append(m.conflicts, fmt.Sprintf("%s:\n%s\n%s", name, previous, signature))
		}
		return name
	}
	m.signatures[name] = signature
	m.queue = append(m.queue, pending{Name: name, Type: typ})
	return name
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
	case *dwarf.ArrayType:
		if typ.Count < 0 {
			return 0
		}
		return typ.Count * m.size(typ.Type)
	default:
		return typ.Size()
	}
}

func integer(size int64, signed bool) string {
	name := fmt.Sprintf("int%d", size*8)
	if !signed {
		name = "u" + name
	}
	return name
}

// goAlign returns the alignment Go gives the view generated for a C type:
// at most a word, one for a union kept as raw bytes, and one for a
// structure member that C packed off its natural alignment.
func (m *generator) goAlign(typ dwarf.Type) int64 {
	switch typ := m.resolved(typ).(type) {
	case *dwarf.StructType:
		if typ.Kind == "union" {
			return 1
		}
		align := int64(1)
		for _, field := range typ.Field {
			if field.BitSize != 0 {
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
	default:
		size := m.size(typ)
		if size <= 0 {
			return 1
		}
		return min(size, 8)
	}
}

func (m *generator) emitStruct(name string, typ *dwarf.StructType) error {
	var body strings.Builder
	fmt.Fprintf(&m.fingerprint, "%s %d;", name, typ.ByteSize)
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
			start := first / 8
			if start < cursor {
				start = cursor
			}
			pad(start)
			size := (last+7)/8 - start
			fieldName := unique(used, fmt.Sprintf("Bits%d", start))
			fmt.Fprintf(&body, "\t%s [%d]byte // bit-fields %s\n", fieldName, size, strings.Join(names, ", "))
			cursor = start + size
			continue
		}

		offset := field.ByteOffset
		size := m.size(field.Type)
		if array, ok := m.underlying(field.Type).(*dwarf.ArrayType); ok && (array.Count < 0 || size == 0) {
			// A flexible array member: no bytes of its own, reached
			// through an accessor sized by the caller.
			fmt.Fprintf(&body, "\t// %s: flexible array at offset %d.\n", field.Name, offset)
			element := m.goType(array.Type, name+camel(field.Name)+"Element")
			m.methods = append(m.methods, fmt.Sprintf(
				"// %[1]s returns the first count elements of the flexible array %[4]s.\n"+
					"func (m *%[2]s) %[1]s(count int) []%[3]s {\n"+
					"\treturn unsafe.Slice((*%[3]s)(unsafe.Add(unsafe.Pointer(m), %[5]d)), count)\n}\n",
				camel(field.Name), name, element, field.Name, offset,
			))
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
		goType := m.goType(field.Type, name+fieldName)
		key := typ.StructName + "." + field.Name
		if (strings.HasPrefix(field.Name, "abs_") && !m.relatives[key]) || m.locals[key] {
			// The dataplane resolves these for its own process.
			goType = "shm.Local"
		}
		pad(offset)
		comment := ""
		if m.isAtomic(field.Type) {
			comment = " // atomic"
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
		fmt.Fprintf(&m.fingerprint, "%s@%d:%d;", field.Name, offset, size)
		cursor = offset + size
	}
	pad(typ.ByteSize)
	body.WriteString("}\n")

	m.views[name] = &view{Source: body.String()}
	m.checks = append(m.checks, check{
		Name: name + " size", Expression: fmt.Sprintf("unsafe.Sizeof(layout.%s{})", name), Want: typ.ByteSize,
	})
	return nil
}

func (m *generator) emitEnum(name string, typ *dwarf.EnumType) {
	base := integer(typ.ByteSize, false)
	for _, value := range typ.Val {
		if value.Val < 0 {
			base = integer(typ.ByteSize, true)
		}
	}
	var body strings.Builder
	fmt.Fprintf(&body, "// %s is the C enum %s.\n", name, typ.EnumName)
	fmt.Fprintf(&body, "type %s %s\n", name, base)
	m.views[name] = &view{Source: body.String()}
	for _, value := range typ.Val {
		constant := camel(strings.ToLower(value.Name))
		if _, taken := m.constants[constant]; taken {
			continue
		}
		m.constants[constant] = fmt.Sprintf("%s %s = %d", constant, name, value.Val)
	}
}

func (m *generator) render(packageName, binary, constraint string) ([]byte, error) {
	sum := sha256.Sum256([]byte(m.fingerprint.String()))
	var out bytes.Buffer
	fmt.Fprintf(&out, "// Code generated by shmgen from the DWARF of %s. DO NOT EDIT.\n\n", binary)
	fmt.Fprintf(&out, "%s\n\n", constraint)
	fmt.Fprintf(&out, "package %s\n\n", packageName)
	out.WriteString("import (\n\t\"unsafe\"\n\n\t\"github.com/yanet-platform/yanet2/controlplane/native/shm\"\n)\n\n")
	out.WriteString("// Fingerprint identifies the layout of every view in this file.\n")
	fmt.Fprintf(&out, "const Fingerprint = %q\n\n", hex.EncodeToString(sum[:8]))

	if len(m.constants) > 0 {
		names := sortedKeys(m.constants)
		out.WriteString("const (\n")
		for _, name := range names {
			fmt.Fprintf(&out, "\t%s\n", m.constants[name])
		}
		out.WriteString(")\n\n")
	}
	for _, name := range sortedKeys(m.views) {
		out.WriteString(m.views[name].Source)
		out.WriteString("\n")
	}
	sort.Strings(m.methods)
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
	fmt.Fprintf(&out, "import (\n\t\"testing\"\n\t\"unsafe\"\n\n\t\"github.com/stretchr/testify/require\"\n\n\t%q\n)\n\n", importPath)
	out.WriteString("// Test_Layout_MatchesDWARF verifies that every view has the size and\n")
	out.WriteString("// field offsets the C compiler gave its structure.\n")
	out.WriteString("func Test_Layout_MatchesDWARF(t *testing.T) {\n")
	out.WriteString("\tcases := []struct {\n\t\tname string\n\t\tgot  uintptr\n\t\twant uintptr\n\t}{\n")
	for _, check := range m.checks {
		fmt.Fprintf(&out, "\t\t{%q, %s, %d},\n", check.Name, check.Expression, check.Want)
	}
	out.WriteString("\t}\n\tfor _, tc := range cases {\n")
	out.WriteString("\t\tt.Run(tc.name, func(t *testing.T) {\n\t\t\trequire.Equal(t, tc.want, tc.got)\n\t\t})\n\t}\n}\n")
	return format.Source(out.Bytes())
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	for _, part := range strings.Split(name, "_") {
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
