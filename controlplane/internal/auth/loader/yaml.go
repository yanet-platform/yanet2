package loader

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// DecodeYAML requires a single document with only known fields in strict mode.
func DecodeYAML(data []byte, target any, strict bool) error {
	if !strict {
		return yaml.Unmarshal(data, target)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(yaml.Node)); err != io.EOF {
		return fmt.Errorf("expected a single YAML document")
	}
	return nil
}
