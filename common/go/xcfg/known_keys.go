package xcfg

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

var yamlNodeType = reflect.TypeFor[yaml.Node]()

// mergeTag is the resolved tag yaml.v3 assigns to a "<<" merge key.
const mergeTag = "!!merge"

// CheckKnownKeys reports every YAML mapping key in data that has no matching
// field in T.
//
// Unlike WithKnownFields, it walks the parsed node tree directly against T's
// reflected shape instead of driving yaml.v3's own strict decoder, so it
// keeps working across a field whose type implements UnmarshalYAML by
// re-decoding through a fresh, non-strict decoder — the case
// WithKnownFields is blind to. All unknown keys are collected into one
// error, each as a dotted path rooted at the document.
func CheckKnownKeys[T any](data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("failed to parse yaml document: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil
	}

	var unknown []string
	walkKnownKeys(doc.Content[0], reflect.TypeFor[T](), "", &unknown, map[*yaml.Node]bool{})
	if len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf("unknown keys: %s", strings.Join(unknown, ", "))
}

// walkKnownKeys matches node's shape against t, appending the dotted path of
// every mapping key that has no corresponding field to unknown.
//
// visiting holds the alias targets already on the current descent path, so
// a self-referential merge anchor is caught here instead of recursing
// forever — yaml.v3 only rejects that cycle when decoding into a Go value,
// not into a node tree, and LoadConfig catches it separately, so stopping
// silently is enough. The entry is removed on return, so a legitimate
// anchor reused at sibling paths is still walked at each site.
func walkKnownKeys(node *yaml.Node, t reflect.Type, path string, unknown *[]string, visiting map[*yaml.Node]bool) {
	if node == nil {
		return
	}
	if node.Kind == yaml.AliasNode {
		target := node.Alias
		if target == nil || visiting[target] {
			return
		}
		visiting[target] = true
		defer delete(visiting, target)
		node = target
	}

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Interface || t == yamlNodeType {
		return
	}

	switch node.Kind {
	case yaml.MappingNode:
		walkMappingNode(node, t, path, unknown, visiting)
	case yaml.SequenceNode:
		walkSequenceNode(node, t, path, unknown, visiting)
	}
}

// walkMappingNode checks a YAML mapping node against a struct or map type.
//
// Any other target type leaves the mapping with no declared keys to check
// against, so it is skipped without reporting — for example a mapping
// value against a plain scalar field is a shape mismatch that yaml.v3
// itself rejects on the strict decode path.
func walkMappingNode(node *yaml.Node, t reflect.Type, path string, unknown *[]string, visiting map[*yaml.Node]bool) {
	switch t.Kind() {
	case reflect.Struct:
		fields, inlineElem := collectFields(t)
		for idx := 0; idx+1 < len(node.Content); idx += 2 {
			keyNode, valueNode := node.Content[idx], node.Content[idx+1]
			if keyNode.Tag == mergeTag {
				walkMergeValue(valueNode, t, path, unknown, visiting)
				continue
			}

			fieldPath := joinPath(path, keyNode.Value)
			fieldType, ok := fields[keyNode.Value]
			switch {
			case ok:
				walkKnownKeys(valueNode, fieldType, fieldPath, unknown, visiting)
			case inlineElem != nil:
				walkKnownKeys(valueNode, inlineElem, fieldPath, unknown, visiting)
			default:
				*unknown = append(*unknown, fieldPath)
			}
		}
	case reflect.Map:
		elemType := t.Elem()
		for idx := 0; idx+1 < len(node.Content); idx += 2 {
			fieldPath := joinPath(path, node.Content[idx].Value)
			walkKnownKeys(node.Content[idx+1], elemType, fieldPath, unknown, visiting)
		}
	}
}

// walkMergeValue walks a "<<" merge key's value against t, the struct type
// of the mapping it merges into, since merged keys land in that mapping
// rather than under a field of their own.
//
// The value can also be a scalar or a sequence of scalars — shapes YAML
// permits, though neither carries keys to check. A sequence value is
// walked in place rather than routed through walkSequenceNode, since its
// elements share t rather than a slice element type.
func walkMergeValue(node *yaml.Node, t reflect.Type, path string, unknown *[]string, visiting map[*yaml.Node]bool) {
	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			walkKnownKeys(item, t, path, unknown, visiting)
		}
		return
	}
	walkKnownKeys(node, t, path, unknown, visiting)
}

// walkSequenceNode checks each element of a YAML sequence node against a
// slice or array type's element type.
func walkSequenceNode(node *yaml.Node, t reflect.Type, path string, unknown *[]string, visiting map[*yaml.Node]bool) {
	if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
		return
	}
	elemType := t.Elem()
	for idx, item := range node.Content {
		walkKnownKeys(item, elemType, fmt.Sprintf("%s[%d]", path, idx), unknown, visiting)
	}
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
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
