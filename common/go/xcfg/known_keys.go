package xcfg

import (
	"encoding"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

var yamlNodeType = reflect.TypeFor[yaml.Node]()

var yamlUnmarshalerType = reflect.TypeFor[yaml.Unmarshaler]()

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// walkableType is implemented by a wrapper whose keys decode into a
// different type than the wrapper itself, such as Optional[T], so the walk
// can check that type's fields instead of stopping at isOpaqueToWalk.
type walkableType interface {
	WalkType() reflect.Type
}

// mergeTag is the resolved tag yaml.v3 assigns to a "<<" merge key.
const mergeTag = "!!merge"

const nullTag = "!!null"

// unknownKey records one mapping key with no matching field, along with the
// document line it sits on, so the reported error can point straight at it.
type unknownKey struct {
	Path string
	Line int
}

type nullValue struct {
	Path string
	Line int
}

type findings struct {
	Unknown []unknownKey
	Nulls   []nullValue
}

// CheckKnownKeys reports every YAML mapping key in data that has no matching
// field in T.
//
// It walks the parsed node tree directly against T's reflected shape
// instead of driving yaml.v3's own strict decoder, so it keeps working
// across a field whose type implements UnmarshalYAML by re-decoding
// through a fresh, non-strict decoder. WithKnownFields runs the same walk
// as part of Decode. Every unknown key is collected into one error, each
// named by its dotted path rooted at the document and the line it sits on.
func CheckKnownKeys[T any](data []byte) error {
	return checkKnownKeys(data, reflect.TypeFor[T]())
}

// checkKnownKeys drives the same walk as CheckKnownKeys against the
// reflect.Type its type parameter resolves to.
func checkKnownKeys(data []byte, t reflect.Type) error {
	collected, err := walkDocument(data, t)
	if err != nil {
		return err
	}
	return unknownKeysError(collected.Unknown)
}

func walkDocument(data []byte, t reflect.Type) (*findings, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	collected := &findings{}
	if len(doc.Content) == 0 {
		return collected, nil
	}

	walkKnownKeys(doc.Content[0], t, "", collected, map[*yaml.Node]bool{})
	return collected, nil
}

func unknownKeysError(unknown []unknownKey) error {
	if len(unknown) == 0 {
		return nil
	}
	if len(unknown) == 1 {
		return fmt.Errorf("unknown key %q specified at line %d", unknown[0].Path, unknown[0].Line)
	}

	parts := make([]string, len(unknown))
	for idx, u := range unknown {
		parts[idx] = fmt.Sprintf("%q at line %d", u.Path, u.Line)
	}
	return fmt.Errorf("unknown keys specified: %s", strings.Join(parts, ", "))
}

func nullValuesError(nulls []nullValue) error {
	if len(nulls) == 0 {
		return nil
	}
	if len(nulls) == 1 {
		return fmt.Errorf("key %q at line %d has no value; give it a body or remove the key", nulls[0].Path, nulls[0].Line)
	}

	parts := make([]string, len(nulls))
	for idx, null := range nulls {
		parts[idx] = fmt.Sprintf("%q at line %d", null.Path, null.Line)
	}
	return fmt.Errorf("keys with no value: %s", strings.Join(parts, ", "))
}

func isTransparentToWalk(t reflect.Type) bool {
	if t.Kind() == reflect.Interface || t == yamlNodeType {
		return false
	}
	if wt, ok := reflect.Zero(t).Interface().(walkableType); ok {
		return isTransparentToWalk(wt.WalkType())
	}
	if isOpaqueToWalk(t) {
		return false
	}
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return false
	}
	return t.Kind() == reflect.Struct || t.Kind() == reflect.Map
}

// walkKnownKeys matches node's shape against t, appending the dotted path of
// every mapping key that has no corresponding field to collected.
//
// visiting holds the alias targets already on the current descent path, so
// a self-referential merge anchor is caught here instead of recursing
// forever — yaml.v3 only rejects that cycle when decoding into a Go value,
// not into a node tree, and LoadConfig catches it separately, so stopping
// silently is enough. The entry is removed on return, so a legitimate
// anchor reused at sibling paths is still walked at each site.
func walkKnownKeys(node *yaml.Node, t reflect.Type, path string, collected *findings, visiting map[*yaml.Node]bool) {
	if node == nil {
		return
	}
	line := node.Line
	if node.Kind == yaml.AliasNode {
		target := node.Alias
		if target == nil || visiting[target] {
			return
		}
		visiting[target] = true
		defer delete(visiting, target)
		node = target
	}

	if node.ShortTag() == nullTag {
		if t.Kind() != reflect.Pointer && isTransparentToWalk(t) {
			collected.Nulls = append(collected.Nulls, nullValue{Path: path, Line: line})
		}
		return
	}

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Interface || t == yamlNodeType {
		return
	}
	if wt, ok := reflect.Zero(t).Interface().(walkableType); ok {
		walkKnownKeys(node, wt.WalkType(), path, collected, visiting)
		return
	}
	if isOpaqueToWalk(t) {
		return
	}

	switch node.Kind {
	case yaml.MappingNode:
		walkMappingNode(node, t, path, collected, visiting)
	case yaml.SequenceNode:
		walkSequenceNode(node, t, path, collected, visiting)
	}
}

type mappingResolver func(key string) (reflect.Type, bool)

// walkMappingNode checks a YAML mapping node against a struct or map type.
//
// Any other target type leaves the mapping with no declared keys to check
// against, so it is skipped without reporting — for example a mapping
// value against a plain scalar field is a shape mismatch that yaml.v3
// itself rejects on the strict decode path.
func walkMappingNode(node *yaml.Node, t reflect.Type, path string, collected *findings, visiting map[*yaml.Node]bool) {
	var resolve mappingResolver
	skipComplexKey := false
	switch t.Kind() {
	case reflect.Struct:
		fields, inlineElem := collectFields(t)
		skipComplexKey = true
		resolve = func(key string) (reflect.Type, bool) {
			if fieldType, ok := fields[key]; ok {
				return fieldType, true
			}
			if inlineElem != nil {
				return inlineElem, true
			}
			return nil, false
		}
	case reflect.Map:
		elemType := t.Elem()
		resolve = func(key string) (reflect.Type, bool) {
			return elemType, true
		}
	default:
		return
	}

	claimed := map[string]bool{}
	mergeValue := walkMappingKeys(node, resolve, skipComplexKey, path, collected, visiting, claimed)
	if mergeValue != nil {
		walkMergeValue(mergeValue, resolve, skipComplexKey, path, collected, visiting, claimed)
	}
}

func isMergeKey(keyNode *yaml.Node) bool {
	if keyNode.Kind != yaml.ScalarNode || keyNode.Value != "<<" {
		return false
	}
	return keyNode.Tag == "" || keyNode.Tag == "!" || keyNode.ShortTag() == mergeTag
}

func mappingKeyName(keyNode *yaml.Node) string {
	if keyNode.Kind == yaml.AliasNode && keyNode.Alias != nil {
		return keyNode.Alias.Value
	}
	return keyNode.Value
}

type mappingKeyEntry struct {
	ValueNode *yaml.Node
	Line      int
}

func walkMappingKeys(node *yaml.Node, resolve mappingResolver, skipComplexKey bool, path string, collected *findings, visiting map[*yaml.Node]bool, claimed map[string]bool) *yaml.Node {
	var mergeValue *yaml.Node
	var order []string
	last := map[string]mappingKeyEntry{}
	for idx := 0; idx+1 < len(node.Content); idx += 2 {
		keyNode, valueNode := node.Content[idx], node.Content[idx+1]
		if isMergeKey(keyNode) {
			mergeValue = valueNode
			continue
		}
		if skipComplexKey && (keyNode.Kind == yaml.SequenceNode || keyNode.Kind == yaml.MappingNode) {
			continue
		}
		key := mappingKeyName(keyNode)
		if !claimed[key] {
			order = append(order, key)
		}
		claimed[key] = true
		last[key] = mappingKeyEntry{ValueNode: valueNode, Line: keyNode.Line}
	}
	for _, key := range order {
		entry := last[key]
		walkMappingEntry(resolve, key, entry.Line, entry.ValueNode, path, collected, visiting)
	}
	return mergeValue
}

func walkMappingEntry(resolve mappingResolver, key string, line int, valueNode *yaml.Node, path string, collected *findings, visiting map[*yaml.Node]bool) {
	fieldPath := joinPath(path, key)
	if fieldType, ok := resolve(key); ok {
		walkKnownKeys(valueNode, fieldType, fieldPath, collected, visiting)
		return
	}
	collected.Unknown = append(collected.Unknown, unknownKey{Path: fieldPath, Line: line})
}

// walkMergeValue walks a "<<" merge key's value, resolving each merged
// key's value against the receiving mapping via resolve, since merged
// keys land in that mapping rather than under a field of their own.
//
// The value can also be a scalar or a sequence of scalars — shapes YAML
// permits, though neither carries keys to check. A sequence value is
// walked in place rather than routed through walkSequenceNode, since its
// elements share resolve rather than a slice element type.
func walkMergeValue(node *yaml.Node, resolve mappingResolver, skipComplexKey bool, path string, collected *findings, visiting map[*yaml.Node]bool, claimed map[string]bool) {
	if node.ShortTag() == nullTag {
		return
	}
	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			if item.ShortTag() == nullTag {
				continue
			}
			walkMergeSource(item, resolve, skipComplexKey, path, collected, visiting, claimed)
		}
		return
	}
	walkMergeSource(node, resolve, skipComplexKey, path, collected, visiting, claimed)
}

func walkMergeSource(node *yaml.Node, resolve mappingResolver, skipComplexKey bool, path string, collected *findings, visiting map[*yaml.Node]bool, claimed map[string]bool) {
	if node.Kind == yaml.AliasNode {
		target := node.Alias
		if target == nil || visiting[target] {
			return
		}
		visiting[target] = true
		defer delete(visiting, target)
		node = target
	}
	if node.Kind != yaml.MappingNode {
		return
	}

	mergeValue := walkMappingKeys(node, resolve, skipComplexKey, path, collected, visiting, claimed)
	if mergeValue != nil {
		walkMergeValue(mergeValue, resolve, skipComplexKey, path, collected, visiting, claimed)
	}
}

// walkSequenceNode checks each element of a YAML sequence node against a
// slice or array type's element type.
func walkSequenceNode(node *yaml.Node, t reflect.Type, path string, collected *findings, visiting map[*yaml.Node]bool) {
	if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
		return
	}
	elemType := t.Elem()
	for idx, item := range node.Content {
		walkKnownKeys(item, elemType, fmt.Sprintf("%s[%d]", path, idx), collected, visiting)
	}
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// isOpaqueToWalk reports whether t is a struct with no exported fields
// whose pointer implements yaml.Unmarshaler.
//
// Such a type decodes entirely through its own method rather than the
// default struct decoder, so the walk has no field set to check a mapping's
// keys against and must leave them unreported. The implements-Unmarshaler
// half of the check keeps a plain struct that only has unexported fields
// and no custom decoding — which really does drop every key silently —
// reported as unknown.
func isOpaqueToWalk(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	for idx := range t.NumField() {
		if t.Field(idx).IsExported() {
			return false
		}
	}
	return reflect.PointerTo(t).Implements(yamlUnmarshalerType)
}

// fieldSet maps a YAML key name to the Go type that decodes it.
type fieldSet map[string]reflect.Type

// collectFields returns t's exported fields keyed by their YAML name, plus
// the element type of at most one inline map field, or nil if there is
// none.
//
// The inline map field catches any key the named fields don't match
// instead of the key being unknown. A field with no explicit tag name is
// keyed by its lowercased Go name, matching yaml.v3's own default key
// resolution. An inline struct field's own fields are merged in at the
// same level.
func collectFields(t reflect.Type) (fieldSet, reflect.Type) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, nil
	}

	fields := fieldSet{}
	var inlineElem reflect.Type
	for idx := range t.NumField() {
		f := t.Field(idx)
		if !f.IsExported() {
			continue
		}

		tag := f.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		if isInlineTag(tag) {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Map {
				inlineElem = ft.Elem()
				continue
			}
			nested, nestedInlineElem := collectFields(f.Type)
			maps.Copy(fields, nested)
			if nestedInlineElem != nil {
				inlineElem = nestedInlineElem
			}
			continue
		}

		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		fields[name] = f.Type
	}
	return fields, inlineElem
}

// isInlineTag reports whether a yaml struct tag carries the "inline" option.
func isInlineTag(tag string) bool {
	_, opts, found := strings.Cut(tag, ",")
	if !found {
		return false
	}
	return slices.Contains(strings.Split(opts, ","), "inline")
}
