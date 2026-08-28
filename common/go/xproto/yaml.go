package xproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

// Unmarshal decodes a single YAML document, spelled as the message's JSON
// form, into the message.
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
	if err := rejectNullEntries(tree, ""); err != nil {
		return err
	}
	encoded, err := json.Marshal(tree)
	if err != nil {
		return fmt.Errorf(
			"the document holds what JSON cannot, such as a non-string mapping key or a NaN: %w", err,
		)
	}

	proto.Reset(msg)
	jsonDecoder := json.NewDecoder(bytes.NewReader(encoded))
	jsonDecoder.DisallowUnknownFields()
	return jsonDecoder.Decode(msg)
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

// rejectNullEntries fails on a null list entry, which would otherwise land
// as an empty value far from the file that caused it.
func rejectNullEntries(node any, path string) error {
	switch value := node.(type) {
	case []any:
		for idx, entry := range value {
			entryPath := fmt.Sprintf("%s[%d]", path, idx)
			if entry == nil {
				return fmt.Errorf("%s is null", entryPath)
			}
			if err := rejectNullEntries(entry, entryPath); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, entry := range value {
			entryPath := key
			if path != "" {
				entryPath = path + "." + key
			}
			if err := rejectNullEntries(entry, entryPath); err != nil {
				return err
			}
		}
	}
	return nil
}
