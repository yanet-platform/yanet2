package xcfg

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

// DefaultEnvPrefix is the prefix every environment override carries.
const DefaultEnvPrefix = "YANET_"

// maxEnvDepth bounds the type walk so a self-referential config type cannot
// recurse forever while names are generated.
const maxEnvDepth = 32

var durationType = reflect.TypeFor[time.Duration]()

// envTyped is implemented by a wrapper that stores its value in an unexported
// field, such as NonEmptyString or NonZero[T], so the env walk can see the
// type actually being configured instead of stopping at the wrapper.
//
// The wrapped type decides two things the wrapper itself cannot answer:
// whether the path is a leaf, and whether the value must be emitted as a
// quoted string.
type envTyped interface {
	EnvType() reflect.Type
}

// envValued exposes a wrapper's current value to the environment overlay.
//
// The value is used only when an absent sequence must retain destination
// defaults before one of its elements is overridden.
type envValued interface {
	EnvValue() reflect.Value
}

type envExplicitlySet interface {
	EnvIsSet() bool
}

type defaultPruneResult uint8

const (
	defaultKept defaultPruneResult = iota
	defaultOmitted
	defaultRejected
)

type envMaterialization struct {
	Node  *yaml.Node
	Value reflect.Value
}

type envApplyState struct {
	Names            map[string]string
	Touched          map[*yaml.Node]struct{}
	Detached         map[*yaml.Node]struct{}
	Materializations []envMaterialization
	Changed          bool
}

// applyEnv overlays environment variables onto the YAML document and returns
// the re-encoded document.
//
// Overriding the document rather than the decoded struct is what keeps the
// wrappers honest: every value still arrives through the type's own
// UnmarshalYAML, so NonEmptyString still rejects an empty string and
// Required still counts as explicitly set. Writing the fields by reflection
// would reach past those unexported fields and skip their validation
// entirely.
//
// The variable name for a field is generated from the destination type, never
// parsed out of the environment, so a key that itself contains an underscore
// (http_endpoint) cannot be split at the wrong place.
//
// Returns buf unchanged when no variable applies, leaving the reported error
// lines pointing at the user's own file.
func applyEnv(buf []byte, dst any, prefix string, env map[string]string) ([]byte, error) {
	t := reflect.TypeOf(dst)
	if t == nil {
		return nil, fmt.Errorf("environment overrides require a non-nil destination")
	}
	if len(env) == 0 {
		return buf, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(buf, &doc); err != nil {
		return nil, err
	}

	root := documentRoot(&doc)
	state := &envApplyState{
		Names:    map[string]string{},
		Touched:  map[*yaml.Node]struct{}{},
		Detached: map[*yaml.Node]struct{}{},
	}

	if err := envApplyNode(
		root,
		t,
		reflect.ValueOf(dst),
		prefix,
		"",
		env,
		state,
		0,
	); err != nil {
		return nil, err
	}
	for _, materialization := range state.Materializations {
		if pruneUnsetRequiredDefaults(
			materialization.Node,
			materialization.Value,
			state.Touched,
		) == defaultRejected {
			return nil, fmt.Errorf("cannot materialize defaults containing an unset required value")
		}
	}
	if !state.Changed {
		return buf, nil
	}
	if err := expandDetachedAliases(root, state.Detached); err != nil {
		return nil, err
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("failed to re-encode config with environment overrides: %w", err)
	}

	return out, nil
}

// environ returns the variables carrying prefix, keyed by full name.
func environ(prefix string) map[string]string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if !found || !strings.HasPrefix(name, prefix) {
			continue
		}
		env[name] = value
	}

	return env
}

// documentRoot returns the document's root node, seeding an empty mapping for
// an empty document so an override can still create the first key.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}

	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	*doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}

	return root
}

// envApplyNode walks t alongside node, writing every variable that names a
// path within it.
//
// envName is the variable name for the current path and path is its dotted
// config equivalent, kept only to report a collision in the terms the config
// file uses.
func envApplyNode(
	node *yaml.Node,
	t reflect.Type,
	value reflect.Value,
	envName string,
	path string,
	env map[string]string,
	state *envApplyState,
	depth int,
) error {
	if depth > maxEnvDepth {
		return fmt.Errorf("config type nests deeper than %d levels at %q", maxEnvDepth, path)
	}

	wrappedValue := envValueIsWrapped(value)
	value = envUnwrapValue(value)
	t = envUnwrap(t)
	if value.IsValid() && value.Type() != t {
		value = reflect.Value{}
	}
	// yaml.Node holds an arbitrary raw YAML value. Its exported implementation
	// fields are not part of the destination schema, so environment overrides
	// cannot address them.
	if t == yamlNodeType {
		return nil
	}

	_, yamlExact := env[envName]
	if yamlExact && envDecodesYAML(t) && envNamesBelow(env, envName) {
		return fmt.Errorf("environment variable %s conflicts with a nested override", envName)
	}
	if (yamlExact && envDecodesYAML(t)) || envIsLeaf(t) {
		value, ok := env[envName]
		if !ok {
			return nil
		}
		// Two distinct config paths folding onto one variable would make
		// the override silently ambiguous, so it is rejected instead.
		if previous, seen := state.Names[envName]; seen && previous != path {
			return fmt.Errorf(
				"environment variable %s maps to both %q and %q", envName, previous, path,
			)
		}
		state.Names[envName] = path

		setEnvScalar(node, t, value)
		state.Touched[node] = struct{}{}
		state.Changed = true

		return nil
	}

	switch t.Kind() {
	case reflect.Struct:
		if wrappedValue && isAbsentNode(node) {
			if err := materializeDefaults(node, value, yaml.MappingNode, state); err != nil {
				return err
			}
		}
		return envApplyStruct(node, t, value, envName, path, env, state, depth)
	case reflect.Slice, reflect.Array:
		return envApplySequence(node, t, value, envName, path, env, state, depth)
	default:
		// A map has no fixed key set to generate names from, so its
		// entries stay file-only.
		return nil
	}
}

func envApplyStruct(
	node *yaml.Node,
	t reflect.Type,
	value reflect.Value,
	envName string,
	path string,
	env map[string]string,
	state *envApplyState,
	depth int,
) error {
	for idx := range t.NumField() {
		f := t.Field(idx)
		if !f.IsExported() {
			continue
		}

		tag := f.Tag.Get("yaml")
		if tag == "-" {
			continue
		}

		// An inline struct's fields live at the parent's level in both
		// the document and the variable name.
		if isInlineTag(tag) {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Map {
				continue
			}
			if err := envApplyNode(
				node,
				f.Type,
				envStructField(value, idx),
				envName,
				path,
				env,
				state,
				depth+1,
			); err != nil {
				return err
			}
			continue
		}

		key := envKeyName(f)
		childEnv := envJoin(envName, envSegment(key))
		// Descending only where a variable names a leaf keeps the
		// document free of empty mappings that would decode as null and
		// wipe a default.
		if !envWrites(f.Type, childEnv, env, depth+1) {
			continue
		}

		// A node of an incompatible shape is left for the decoder to
		// reject, so an override cannot turn a malformed file into a
		// valid one.
		if !ensureMapping(node, state) {
			return nil
		}
		child := mappingChild(node, key, state)

		childPath := key
		if path != "" {
			childPath = path + "." + key
		}
		if err := envApplyNode(
			child,
			f.Type,
			envStructField(value, idx),
			childEnv,
			childPath,
			env,
			state,
			depth+1,
		); err != nil {
			return err
		}
	}

	return nil
}

func envApplySequence(
	node *yaml.Node,
	t reflect.Type,
	value reflect.Value,
	envName string,
	path string,
	env map[string]string,
	state *envApplyState,
	depth int,
) error {
	indices := envIndices(env, envName)
	// An index a fixed array has no element for names nothing in the
	// destination type, the same as a variable that matches no field at
	// all. Appending it would grow the sequence past the array's length
	// and make yaml.v3 reject an otherwise valid file.
	indices = envIndicesInRange(indices, t)
	// An index counts only when a variable names a leaf inside that
	// element. A name that merely looks like an index would otherwise
	// append an element no leaf ever fills, which decodes as null.
	indices = slices.DeleteFunc(indices, func(idx int) bool {
		return !envWrites(t.Elem(), envJoin(envName, strconv.Itoa(idx)), env, depth+1)
	})
	if len(indices) == 0 {
		return nil
	}

	wasAbsent := isAbsentNode(node)
	if wasAbsent {
		if err := materializeDefaults(node, value, yaml.SequenceNode, state); err != nil {
			return err
		}
	}
	// A node of an incompatible shape is left for the decoder to reject,
	// so an override cannot turn a malformed file into a valid one.
	if !ensureSequence(node, state) {
		return nil
	}
	existing := len(node.Content)
	highest := indices[len(indices)-1]

	// An element past the end of the file's list is only well defined when
	// every index up to it is supplied; otherwise the gap would decode as a
	// null element.
	for idx := existing; idx <= highest; idx++ {
		if !slices.Contains(indices, idx) {
			return fmt.Errorf(
				"%s is missing: overriding %s requires every index up to it",
				envJoin(envName, strconv.Itoa(idx)),
				envJoin(envName, strconv.Itoa(highest)),
			)
		}
		node.Content = append(node.Content, &yaml.Node{})
	}

	for _, idx := range indices {
		elemEnv := envJoin(envName, strconv.Itoa(idx))
		elemPath := fmt.Sprintf("%s[%d]", path, idx)
		if err := envApplyNode(
			node.Content[idx],
			t.Elem(),
			envSequenceElement(value, idx),
			elemEnv,
			elemPath,
			env,
			state,
			depth+1,
		); err != nil {
			return err
		}
	}

	return nil
}

// materializeDefaults seeds an absent container from the destination.
//
// Decode may receive a config whose defaults already populate a sequence. A
// generated sequence must start from those elements so overriding one index
// does not erase sibling fields, later elements, or reject a later index as a
// gap. A struct behind a value wrapper needs the same treatment because its
// decoder replaces the whole wrapped value rather than updating it in place.
func materializeDefaults(
	node *yaml.Node,
	value reflect.Value,
	kind yaml.Kind,
	state *envApplyState,
) error {
	if !value.IsValid() || !value.CanInterface() {
		return nil
	}

	buf, err := yaml.Marshal(value.Interface())
	if err != nil {
		return fmt.Errorf("failed to encode defaults for environment overrides: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(buf, &doc); err != nil {
		return fmt.Errorf("failed to decode defaults for environment overrides: %w", err)
	}
	root := documentRoot(&doc)
	if root.Kind == kind {
		materialized := copyNode(root)
		materialized.Anchor = node.Anchor
		*node = *materialized
		markDetachedTree(node, state.Detached)
		state.Materializations = append(state.Materializations, envMaterialization{
			Node:  node,
			Value: value,
		})
	}

	return nil
}

// pruneUnsetRequiredDefaults keeps explicit-presence validation intact.
//
// A YAML round-trip would otherwise serialize an unset required wrapper as
// its zero value and decode it back as explicitly supplied. Mapping fields can
// be omitted; a positional or wrapper-nested value cannot be represented
// without changing its meaning, so materialization rejects it.
func pruneUnsetRequiredDefaults(
	node *yaml.Node,
	value reflect.Value,
	touched map[*yaml.Node]struct{},
) defaultPruneResult {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return defaultKept
		}
		value = value.Elem()
	}
	if !value.IsValid() || !value.CanInterface() {
		return defaultKept
	}
	if explicit, ok := value.Interface().(envExplicitlySet); ok && !explicit.EnvIsSet() {
		if !envNodeTouched(node, touched) {
			return defaultOmitted
		}
	}
	if wrapper, ok := value.Interface().(envValued); ok {
		switch pruneUnsetRequiredDefaults(node, wrapper.EnvValue(), touched) {
		case defaultOmitted, defaultRejected:
			return defaultRejected
		}
		return defaultKept
	}

	switch value.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return defaultKept
		}
		valueType := value.Type()
		for fieldIdx := range valueType.NumField() {
			field := valueType.Field(fieldIdx)
			if !field.IsExported() || field.Tag.Get("yaml") == "-" {
				continue
			}
			if isInlineTag(field.Tag.Get("yaml")) {
				if pruneUnsetRequiredDefaults(node, value.Field(fieldIdx), touched) != defaultKept {
					return defaultRejected
				}
				continue
			}

			key := envKeyName(field)
			for nodeIdx := 0; nodeIdx+1 < len(node.Content); nodeIdx += 2 {
				if mappingKeyName(node.Content[nodeIdx]) != key {
					continue
				}
				switch pruneUnsetRequiredDefaults(
					node.Content[nodeIdx+1],
					value.Field(fieldIdx),
					touched,
				) {
				case defaultOmitted:
					node.Content = slices.Delete(node.Content, nodeIdx, nodeIdx+2)
				case defaultRejected:
					return defaultRejected
				}
				break
			}
		}
	case reflect.Slice, reflect.Array:
		if node.Kind != yaml.SequenceNode {
			return defaultKept
		}
		for idx := range min(value.Len(), len(node.Content)) {
			if pruneUnsetRequiredDefaults(node.Content[idx], value.Index(idx), touched) != defaultKept {
				return defaultRejected
			}
		}
	}

	return defaultKept
}

func envNodeTouched(node *yaml.Node, touched map[*yaml.Node]struct{}) bool {
	if _, ok := touched[node]; ok {
		return true
	}
	for _, child := range node.Content {
		if envNodeTouched(child, touched) {
			return true
		}
	}

	return false
}

func envValueIsWrapped(value reflect.Value) bool {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if !value.IsValid() || !value.CanInterface() {
		return false
	}

	_, ok := value.Interface().(envValued)
	return ok
}

func envUnwrapValue(value reflect.Value) reflect.Value {
	for range maxEnvDepth {
		for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
			if value.IsNil() {
				return reflect.Value{}
			}
			value = value.Elem()
		}
		if !value.IsValid() || !value.CanInterface() {
			return value
		}

		wrapper, ok := value.Interface().(envValued)
		if !ok {
			return value
		}
		value = wrapper.EnvValue()
	}

	return value
}

func envStructField(value reflect.Value, idx int) reflect.Value {
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return reflect.Value{}
	}

	return value.Field(idx)
}

func envSequenceElement(value reflect.Value, idx int) reflect.Value {
	if !value.IsValid() || value.Kind() != reflect.Slice && value.Kind() != reflect.Array || idx >= value.Len() {
		return reflect.Value{}
	}

	return value.Index(idx)
}

// envIndicesInRange drops the indices a fixed array has no element for.
// A slice grows to fit, so every index stays.
func envIndicesInRange(indices []int, t reflect.Type) []int {
	if t.Kind() != reflect.Array {
		return indices
	}

	return slices.DeleteFunc(indices, func(idx int) bool {
		return idx >= t.Len()
	})
}

// envIndices returns the sorted element indices addressed under envName.
func envIndices(env map[string]string, envName string) []int {
	prefix := envChildPrefix(envName)

	var indices []int
	for name := range env {
		rest, found := strings.CutPrefix(name, prefix)
		if !found {
			continue
		}

		digits, _, _ := strings.Cut(rest, "_")
		idx, err := strconv.Atoi(digits)
		if err != nil || idx < 0 {
			continue
		}
		if !slices.Contains(indices, idx) {
			indices = append(indices, idx)
		}
	}
	slices.Sort(indices)

	return indices
}

// envWrites reports whether any variable names a leaf inside t.
//
// A variable is matched against the names t itself generates, never against a
// string prefix. Two sibling keys where one is a prefix of the other are the
// reason: YANET_GATEWAY_DEVICES_0 belongs to gateway_devices, yet it opens
// with every character of the name gateway generates. Descending on that
// resemblance would materialise gateway as an empty mapping that no leaf ever
// fills, which decodes as null and wipes the whole subtree's defaults — and
// with it the config, since a null key is rejected. The same holds for a
// variable that names nothing in the type at all.
//
// A container whose name no variable continues is answered without looking
// at its fields. That is what bounds the cost on a self-referential type: a
// branching one, Node{Left, Right *Node}, would otherwise be enumerated along
// both arms down to the depth bound, which is 2^maxEnvDepth calls.
//
// Past the depth bound the generated names run out before a self-referential
// type does, so the same question is all that is left: a variable that
// continues the current name lets the applying walk reach the bound and
// report the path it gave up on, while an unrelated one leaves the type
// alone.
func envWrites(t reflect.Type, envName string, env map[string]string, depth int) bool {
	if depth > maxEnvDepth {
		_, exact := env[envName]
		return exact || envNamesBelow(env, envName)
	}

	t = envUnwrap(t)
	if t == yamlNodeType {
		return false
	}
	if envDecodesYAML(t) {
		if _, ok := env[envName]; ok {
			return true
		}
	}

	if envIsLeaf(t) {
		_, ok := env[envName]
		return ok
	}

	// Every name a container generates extends the current one, so with
	// no variable below it none of its fields can match. Answering here
	// keeps a branching self-referential type — Node{Left, Right *Node} —
	// from being enumerated to the depth bound along both arms, which
	// costs 2^maxEnvDepth calls and never terminates in practice.
	if !envNamesBelow(env, envName) {
		return false
	}

	switch t.Kind() {
	case reflect.Struct:
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
					continue
				}
				if envWrites(f.Type, envName, env, depth+1) {
					return true
				}
				continue
			}

			childEnv := envJoin(envName, envSegment(envKeyName(f)))
			if envWrites(f.Type, childEnv, env, depth+1) {
				return true
			}
		}

		return false
	case reflect.Slice, reflect.Array:
		// The bound a fixed array puts on its indices holds here too:
		// an index it has no element for names nothing in the type, so
		// reporting a match would materialise a sequence node the
		// applying walk then declines to fill.
		for _, idx := range envIndicesInRange(envIndices(env, envName), t) {
			if envWrites(t.Elem(), envJoin(envName, strconv.Itoa(idx)), env, depth+1) {
				return true
			}
		}

		return false
	default:
		// A map has no fixed key set to generate names from.
		return false
	}
}

// envNamesBelow reports whether any variable continues envName, naming
// something nested below the path it stands for.
func envNamesBelow(env map[string]string, envName string) bool {
	prefix := envChildPrefix(envName)
	for name := range env {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}

// envKeyName returns the YAML key a field decodes from, matching yaml.v3's own
// default of lowercasing an untagged field name.
func envKeyName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	if name == "" {
		name = strings.ToLower(f.Name)
	}

	return name
}

// envJoin appends one segment to a variable name.
//
// An empty base is the unprefixed namespace WithEnvPrefix("") asks for, where
// a top-level key is named NAME rather than _NAME.
func envJoin(envName, segment string) string {
	if envName == "" {
		return segment
	}
	if strings.HasSuffix(envName, "_") {
		return envName + segment
	}

	return envName + "_" + segment
}

// envChildPrefix returns the string every variable naming something below
// envName starts with. Under an empty base that is every variable collected.
func envChildPrefix(envName string) string {
	if envName == "" {
		return ""
	}
	if strings.HasSuffix(envName, "_") {
		return envName
	}

	return envName + "_"
}

// envSegment renders one config key as a variable name segment.
func envSegment(key string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToUpper(r)
		}
		return '_'
	}, key)
}

// envUnwrap resolves a pointer or value wrapper to the type actually being
// configured.
func envUnwrap(t reflect.Type) reflect.Type {
	for range maxEnvDepth {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}

		wrapper, ok := reflect.New(t).Interface().(envTyped)
		if !ok {
			return t
		}

		inner := wrapper.EnvType()
		if inner == nil || inner == t {
			return t
		}
		t = inner
	}

	return t
}

// envIsLeaf reports whether t takes a single scalar value rather than being
// walked into.
func envIsLeaf(t reflect.Type) bool {
	if t == durationType || envDecodesText(t) {
		return true
	}

	switch t.Kind() {
	case reflect.Struct:
		// A custom decoder with no exported schema fields takes one scalar.
		// A decoder with exported fields may still own a mapping, so exact
		// parent variables are handled separately while nested names walk
		// those fields.
		return isOpaqueToWalk(t)
	case reflect.Slice, reflect.Array, reflect.Map:
		return false
	default:
		return true
	}
}

// envDecodesText reports whether t parses itself from a string.
func envDecodesText(t reflect.Type) bool {
	return reflect.PointerTo(t).Implements(textUnmarshalerType)
}

func envDecodesYAML(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && reflect.PointerTo(t).Implements(yamlUnmarshalerType)
}

// setEnvScalar replaces node with the value read from the environment.
//
// The tag is chosen from the destination type because YAML would otherwise
// resolve the most common value in this config — an endpoint such as
// "[::1]:8080" — as a flow sequence, while quoting every value would break a
// numeric field that cannot accept a string.
//
// An anchor the node defines is carried over, since the document's aliases
// resolve to this very node and dropping it would leave every one of them
// dangling. The packaged control plane config relies on this: memory_path
// defines an anchor that a dozen module entries alias.
func setEnvScalar(node *yaml.Node, t reflect.Type, value string) {
	*node = yaml.Node{Kind: yaml.ScalarNode, Value: value, Anchor: node.Anchor}

	if t.Kind() == reflect.String || t == durationType || envDecodesText(t) {
		node.Tag = "!!str"
		node.Style = yaml.DoubleQuotedStyle
	}
}

// ensureMapping turns an absent node into an empty mapping, keeping any
// anchor the document binds to it, and reports whether the node can hold
// one.
//
// A node the file already fills with something else is left untouched: the
// document is malformed for this destination type, and rewriting it would
// let an override that names one field paper over the whole broken value,
// turning an error into a silent success.
func ensureMapping(node *yaml.Node, state *envApplyState) bool {
	resolveForWrite(node, state)
	if node.Kind == yaml.MappingNode {
		return true
	}
	if !isAbsentNode(node) {
		return false
	}

	*node = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Anchor: node.Anchor}

	return true
}

// ensureSequence turns an absent node into an empty sequence, keeping any
// anchor the document binds to it, and reports whether the node can hold
// one. A node of another shape is left for the decoder to reject, as in
// ensureMapping.
func ensureSequence(node *yaml.Node, state *envApplyState) bool {
	resolveForWrite(node, state)
	if node.Kind == yaml.SequenceNode {
		return true
	}
	if !isAbsentNode(node) {
		return false
	}

	*node = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Anchor: node.Anchor}

	return true
}

// isAbsentNode reports whether the node carries no value from the file, so
// materialising a container in its place invents nothing the user wrote.
//
// A zero node is a key the document never had; an explicit null is a key
// written with no value, which a container override is allowed to fill.
func isAbsentNode(node *yaml.Node) bool {
	return node.Kind == 0 || node.Tag == "!!null"
}

// resolveForWrite replaces an alias with a copy of its target so an override
// cannot leak into every other user of the same anchor.
func resolveForWrite(node *yaml.Node, state *envApplyState) {
	if node.Kind != yaml.AliasNode || node.Alias == nil {
		return
	}

	*node = *copyNode(node.Alias)
	markDetachedTree(node, state.Detached)
}

// copyNode deep-copies a node without sharing internal aliases.
//
// Internal alias targets are rewired to copied nodes. Before encoding, those
// aliases are expanded from the final copied values, avoiding duplicate anchor
// names while preserving overrides made after the copy. External aliases keep
// pointing at their original anchors.
func copyNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	copies := map[*yaml.Node]*yaml.Node{}
	out := copyNodeTree(node, copies)
	rewireCopiedAliases(node, out, copies)

	return out
}

func copyNodeTree(node *yaml.Node, copies map[*yaml.Node]*yaml.Node) *yaml.Node {
	out := *node
	out.Anchor = ""
	out.Content = nil
	copies[node] = &out
	for _, child := range node.Content {
		out.Content = append(out.Content, copyNodeTree(child, copies))
	}

	return &out
}

func rewireCopiedAliases(original, copied *yaml.Node, copies map[*yaml.Node]*yaml.Node) {
	if original.Kind == yaml.AliasNode && original.Alias != nil {
		if target, ok := copies[original.Alias]; ok {
			copied.Alias = target
		}
	}
	for idx := range min(len(original.Content), len(copied.Content)) {
		rewireCopiedAliases(original.Content[idx], copied.Content[idx], copies)
	}
}

func markDetachedTree(node *yaml.Node, detached map[*yaml.Node]struct{}) {
	detached[node] = struct{}{}
	for _, child := range node.Content {
		markDetachedTree(child, detached)
	}
}

func expandDetachedAliases(node *yaml.Node, detached map[*yaml.Node]struct{}) error {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		if _, ok := detached[node.Alias]; ok {
			expanded, err := copyExpandedNode(node.Alias, detached, 0)
			if err != nil {
				return err
			}
			*node = *expanded
			return nil
		}
	}
	for _, child := range node.Content {
		if err := expandDetachedAliases(child, detached); err != nil {
			return err
		}
	}

	return nil
}

func copyExpandedNode(node *yaml.Node, detached map[*yaml.Node]struct{}, depth int) (*yaml.Node, error) {
	if depth > maxEnvDepth {
		return nil, fmt.Errorf("environment override alias expansion exceeds %d levels", maxEnvDepth)
	}
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		if _, ok := detached[node.Alias]; ok {
			return copyExpandedNode(node.Alias, detached, depth+1)
		}
	}

	out := *node
	out.Anchor = ""
	out.Content = nil
	for _, child := range node.Content {
		copied, err := copyExpandedNode(child, detached, depth)
		if err != nil {
			return nil, err
		}
		out.Content = append(out.Content, copied)
	}

	return &out, nil
}

// mappingChild returns the value node stored under key, appending one when
// the document does not have that key yet.
//
// A key the mapping holds only through a "<<" merge is materialised as a
// direct key carrying a copy of the inherited value. A direct key overrides
// what the merge supplies, so writing into a fresh empty node would shadow
// the merged mapping whole, dropping every other field it carries.
func mappingChild(node *yaml.Node, key string, state *envApplyState) *yaml.Node {
	for idx := 0; idx+1 < len(node.Content); idx += 2 {
		if mappingKeyName(node.Content[idx]) == key {
			return node.Content[idx+1]
		}
	}

	value := &yaml.Node{}
	if inherited := mergedValue(node, key, 0); inherited != nil {
		value = copyNode(inherited)
		markDetachedTree(value, state.Detached)
	}
	node.Content = append(
		node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)

	return value
}

// mergedValue returns the value a "<<" merge key supplies for key, or nil
// when the mapping inherits no such key.
func mergedValue(node *yaml.Node, key string, depth int) *yaml.Node {
	if depth > maxEnvDepth {
		return nil
	}

	for idx := 0; idx+1 < len(node.Content); idx += 2 {
		if isMergeKey(node.Content[idx]) {
			return mergedSourceValue(node.Content[idx+1], key, depth)
		}
	}

	return nil
}

// mergedSourceValue resolves one merge source, which is a mapping, an alias
// to one, or a sequence of those. The first source holding the key wins, as
// it does when yaml.v3 applies the merge itself.
func mergedSourceValue(node *yaml.Node, key string, depth int) *yaml.Node {
	if node == nil || depth > maxEnvDepth {
		return nil
	}

	if node.Kind == yaml.AliasNode {
		if node.Alias == nil {
			return nil
		}
		node = node.Alias
	}

	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			if value := mergedSourceValue(item, key, depth+1); value != nil {
				return value
			}
		}

		return nil
	}

	if node.Kind != yaml.MappingNode {
		return nil
	}

	for idx := 0; idx+1 < len(node.Content); idx += 2 {
		if mappingKeyName(node.Content[idx]) == key {
			return node.Content[idx+1]
		}
	}

	// A merged mapping may itself inherit the key from a further merge.
	return mergedValue(node, key, depth+1)
}
