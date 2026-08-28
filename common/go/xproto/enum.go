// Package xproto holds protobuf helpers shared by the Go control plane.
//
// Generated enums carry no JSON form of their own, so the helpers here give
// them one: the declared name on output, a name or a number on input.
package xproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Enum is the shape of every generated enum type: an int32 that knows its
// descriptor.
type Enum interface {
	~int32
	protoreflect.Enum
}

// MarshalEnumJSON renders an enum value as its declared name, or as the bare
// number when no name is declared for it.
//
// The number form keeps a value from a newer peer round-tripping through a
// binary that does not know it yet, which is what UnmarshalEnumJSON accepts
// in turn.
func MarshalEnumJSON[E Enum](value E) ([]byte, error) {
	if declared := value.Descriptor().Values().ByNumber(value.Number()); declared != nil {
		return json.Marshal(string(declared.Name()))
	}
	return json.Marshal(int32(value))
}

// UnmarshalEnumJSON reads an enum value written as its declared name or as
// a number into target.
//
// A name must match a declared value exactly. Any number in the int32 range
// is accepted, as proto3 enums are open, so a value unknown to this binary
// is preserved rather than rejected. A JSON null leaves target untouched,
// the convention encoding/json expects from an unmarshaler.
func UnmarshalEnumJSON[E Enum](data []byte, target *E) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return err
	}

	descriptor := E(0).Descriptor()
	switch value := raw.(type) {
	case string:
		declared := descriptor.Values().ByName(protoreflect.Name(value))
		if declared == nil {
			return fmt.Errorf(
				"%q is not a value of %s, want one of %s",
				value, descriptor.FullName(), strings.Join(enumNames(descriptor), ", "),
			)
		}
		*target = E(declared.Number())
		return nil
	case json.Number:
		number, err := strconv.ParseInt(value.String(), 10, 32)
		if err != nil {
			return fmt.Errorf("%s is not a valid number for %s", value, descriptor.FullName())
		}
		*target = E(number)
		return nil
	default:
		return fmt.Errorf(
			"expected a name or a number for %s, got %s", descriptor.FullName(), bytes.TrimSpace(data),
		)
	}
}

func enumNames(descriptor protoreflect.EnumDescriptor) []string {
	values := descriptor.Values()
	names := make([]string, values.Len())
	for idx := range values.Len() {
		names[idx] = string(values.Get(idx).Name())
	}
	return names
}
