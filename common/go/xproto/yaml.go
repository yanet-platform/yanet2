package xproto

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

// Unmarshal decodes a single YAML document, spelled as the message's JSON
// form, into the message.
//
// A key must spell a field name exactly, letter case included, and an error
// found while decoding into the message names the offending value by its
// JSON pointer.
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
	return json.Unmarshal(
		encoded, msg,
		json.RejectUnknownMembers(true),
		json.WithUnmarshalers(json.UnmarshalFromFunc(unmarshalFieldless)),
	)
}

// unmarshalFieldless decodes a message without fields, whose Go struct
// encoding/json/v2 refuses for having no exported fields.
//
// An empty object fills it and a member is unknown. Any other message, or
// a value other than an object, goes to the default decoding.
func unmarshalFieldless(decoder *jsontext.Decoder, message proto.Message) error {
	if message.ProtoReflect().Descriptor().Fields().Len() != 0 || decoder.PeekKind() != '{' {
		return errors.ErrUnsupported
	}
	if _, err := decoder.ReadToken(); err != nil {
		return err
	}
	if decoder.PeekKind() != '}' {
		name, err := decoder.ReadToken()
		if err != nil {
			return err
		}
		return fmt.Errorf("unknown object member name %q", name.String())
	}
	_, err := decoder.ReadToken()
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
