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

// Unmarshal decodes a single YAML document into msg.
//
// The document spells the message in its JSON form, with proto field names
// as keys. Whatever the message cannot hold — an unknown key, a second
// non-empty document, a value of the wrong kind — is an error that leaves
// msg unusable. A null leaves its field at the zero value, and an unquoted
// value keeps the type YAML gives it, so a bare date reaches a string field
// as a timestamp and a bare number does not reach it at all.
func Unmarshal(data []byte, msg proto.Message) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var tree any
	if err := decoder.Decode(&tree); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	for {
		var extra any
		err := decoder.Decode(&extra)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if extra != nil {
			return errors.New("the stream holds more than one document")
		}
	}

	proto.Reset(msg)
	if tree == nil {
		return nil
	}

	encoded, err := json.Marshal(tree)
	if err != nil {
		return fmt.Errorf(
			"the document holds what JSON cannot, such as a non-string mapping key or a NaN: %w", err,
		)
	}
	jsonDecoder := json.NewDecoder(bytes.NewReader(encoded))
	jsonDecoder.DisallowUnknownFields()
	return jsonDecoder.Decode(msg)
}
