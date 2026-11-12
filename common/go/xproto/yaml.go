package xproto

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
	"gopkg.in/yaml.v3"
)

// Unmarshal decodes a single YAML document, spelled as the message's JSON
// form, into the message.
//
// Known keys use the exact field name, including letter case. Unknown keys
// are ignored, and invalid known values report their JSON pointer.
//
// A dynamic message, such as one built from a module's descriptor set, is
// read by its descriptor with the same rules. A part of it whose linked Go
// type has a JSON form of its own, such as an IP address, is read by that
// form, and refused when the form would drop a field of the set's message.
// An enum is read by its declared name or a number, and a oneof member by
// its field name.
func Unmarshal(data []byte, msg proto.Message) error {
	if msg == nil || !msg.ProtoReflect().IsValid() {
		return errors.New("the target message is nil")
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var tree any
	if err := decoder.Decode(&tree); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	for {
		var extra yaml.Node
		err := decoder.Decode(&extra)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if !isEmptyDocument(&extra) {
			return errors.New("the stream holds more than one document")
		}
	}

	if tree == nil {
		proto.Reset(msg)
		return nil
	}
	if err := rejectInvalidEntries(tree, ""); err != nil {
		return err
	}
	// Sorted keys make the reported error the same from run to run.
	encoded, err := json.Marshal(tree, json.Deterministic(true))
	if err != nil {
		return fmt.Errorf(
			"the document holds what JSON cannot, such as a non-string mapping key or a NaN: %w", err,
		)
	}

	proto.Reset(msg)
	if dynamic, ok := msg.(*dynamicpb.Message); ok {
		return unmarshalDynamic(encoded, dynamic)
	}
	return json.Unmarshal(
		encoded, msg,
		json.WithUnmarshalers(json.UnmarshalFromFunc(unmarshalFieldless)),
	)
}

// unmarshalFieldless decodes a message without fields, whose Go struct
// encoding/json/v2 refuses for having no exported fields.
//
// Object members are ignored. Other messages and non-object values use the
// default decoding.
func unmarshalFieldless(decoder *jsontext.Decoder, message proto.Message) error {
	if message.ProtoReflect().Descriptor().Fields().Len() != 0 || decoder.PeekKind() != '{' {
		return errors.ErrUnsupported
	}
	_, err := decoder.ReadValue()
	return err
}

// isEmptyDocument tells a bare separator, which the parser reports as an
// unstyled null, from a document that spells a value.
func isEmptyDocument(node *yaml.Node) bool {
	if node.Kind != yaml.DocumentNode || len(node.Content) != 1 {
		return len(node.Content) == 0
	}
	content := node.Content[0]
	return content.Kind == yaml.ScalarNode && content.Tag == "!!null" &&
		content.Value == "" && content.Style == 0
}

// rejectInvalidEntries fails on a null list entry or a non-string mapping key.
//
// A null entry would otherwise land as an empty value far from the file that
// caused it. JSON has no non-string keys, and since Go 1.27 its encoder turns
// a numeric key into a string instead of failing, so a key such as 443 must
// be refused before encoding.
func rejectInvalidEntries(node any, path string) error {
	switch value := node.(type) {
	case []any:
		for idx, entry := range value {
			entryPath := fmt.Sprintf("%s[%d]", path, idx)
			if entry == nil {
				return fmt.Errorf("%s is null", entryPath)
			}
			if err := rejectInvalidEntries(entry, entryPath); err != nil {
				return err
			}
		}
	case map[any]any:
		if path == "" {
			return errors.New("the document has a non-string mapping key")
		}
		return fmt.Errorf("%s has a non-string mapping key", path)
	case map[string]any:
		for key, entry := range value {
			entryPath := key
			if path != "" {
				entryPath = path + "." + key
			}
			if err := rejectInvalidEntries(entry, entryPath); err != nil {
				return err
			}
		}
	}
	return nil
}
