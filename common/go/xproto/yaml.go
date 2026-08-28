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
// The document is re-encoded as JSON and read through encoding/json, so
// every type keeps its own JSON form: a bare string for a commonpb
// address, a name or a number for an enum with a JSON form. Keys are proto
// field names matched without regard to letter case, an unknown key or a
// second non-empty document is an error, a null leaves its field at the
// zero value, and YAML tags are resolved before a scalar reaches a string
// field. A failure before the JSON stage leaves msg untouched, one inside
// it leaves msg partly written.
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
