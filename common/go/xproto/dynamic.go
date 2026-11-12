package xproto

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

// unmarshalDynamic decodes the JSON text into a dynamic message by walking
// its descriptor.
//
// The walk reads the text a generated message would read, with the rules
// encoding/json/v2 applies to it: each scalar is given to encoding/json/v2
// as the Go type a generated message holds it in, so numbers, bytes, nulls
// and the pointers in errors come out alike on both paths.
func unmarshalDynamic(encoded []byte, message *dynamicpb.Message) error {
	decoder := &dynamicDecoder{
		Decoder: jsontext.NewDecoder(bytes.NewReader(encoded)),
		forms:   map[protoreflect.FullName]jsonForm{},
	}
	return decodeMessageValue(decoder, message)
}

// dynamicDecoder reads the JSON text of one document into a dynamic
// message.
type dynamicDecoder struct {
	*jsontext.Decoder
	// forms remembers, by message, how its values are read, decided on the
	// first value of the message.
	forms map[protoreflect.FullName]jsonForm
}

// jsonForm tells how the values of one message of the descriptor set are
// read.
type jsonForm struct {
	// Linked is the linked Go type whose JSON form reads the message, nil
	// when the descriptor reads it.
	Linked protoreflect.MessageType
	// Err refuses the message, whose linked JSON form would drop a field of
	// the set.
	Err error
}

// Form decides how the values of the message are read: by the JSON form of
// its linked Go type when that type has one, else by the descriptor.
//
// A JSON form that does not read every field of the set's message is
// refused rather than used, so a set newer than this binary loses no field
// in silence.
func (m *dynamicDecoder) Form(message protoreflect.MessageDescriptor) jsonForm {
	if form, ok := m.forms[message.FullName()]; ok {
		return form
	}
	var form jsonForm
	if linked := jsonFormType(message.FullName()); linked != nil {
		form.Linked = linked
		form.Err = coverFields(message.FullName(), message, linked.Descriptor(), map[protoreflect.FullName]bool{})
	}
	m.forms[message.FullName()] = form
	return form
}

// coverFields reports the first field of the set's message that the linked
// message lacks, looking into the messages of its fields too, since a JSON
// form reads its whole value.
func coverFields(
	form protoreflect.FullName,
	set protoreflect.MessageDescriptor,
	linked protoreflect.MessageDescriptor,
	seen map[protoreflect.FullName]bool,
) error {
	if seen[set.FullName()] {
		return nil
	}
	seen[set.FullName()] = true

	fields := set.Fields()
	for idx := range fields.Len() {
		field := fields.Get(idx)
		match := linked.Fields().ByNumber(field.Number())
		if match == nil || match.Name() != field.Name() || match.Kind() != field.Kind() ||
			match.Cardinality() != field.Cardinality() {
			return fmt.Errorf(
				"the JSON form of %s in this binary does not read field %s of the descriptor set",
				form, field.FullName(),
			)
		}
		if field.Message() == nil {
			continue
		}
		if err := coverFields(form, field.Message(), match.Message(), seen); err != nil {
			return err
		}
	}
	return nil
}

// decodeMessageValue reads a message value into the message, as its form
// tells.
//
// A null leaves the message empty, as it leaves a generated message nil.
func decodeMessageValue(decoder *dynamicDecoder, message protoreflect.Message) error {
	if decoder.PeekKind() == 'n' {
		_, err := decoder.ReadToken()
		return err
	}
	form := decoder.Form(message.Descriptor())
	if form.Err != nil {
		if _, err := decoder.ReadValue(); err != nil {
			return err
		}
		return fmt.Errorf("%w within %q", form.Err, decoder.StackPointer())
	}
	if form.Linked == nil {
		return decodeMessage(decoder, message)
	}

	decoded := form.Linked.New().Interface()
	if err := json.UnmarshalDecode(decoder.Decoder, decoded); err != nil {
		return err
	}
	// The value reaches the dynamic message through its encoding, so no
	// part of the message holds a type of another descriptor, which
	// proto.Clone and proto.Merge refuse.
	encodedValue, err := proto.Marshal(decoded)
	if err != nil {
		return err
	}
	return proto.Unmarshal(encodedValue, message.Interface())
}

// jsonFormType finds the linked Go type of a message when that type has a
// JSON form of its own, such as an IP address spelled as text.
func jsonFormType(name protoreflect.FullName) protoreflect.MessageType {
	linked, err := protoregistry.GlobalTypes.FindMessageByName(name)
	if err != nil {
		return nil
	}
	switch linked.Zero().Interface().(type) {
	case json.Unmarshaler, json.UnmarshalerFrom:
		return linked
	}
	return nil
}

// decodeMessage reads a JSON object into the message, field by field.
//
// A member whose name no field of the descriptor has is skipped, as
// encoding/json/v2 skips it for a generated message.
func decodeMessage(decoder *dynamicDecoder, message protoreflect.Message) error {
	if decoder.PeekKind() != '{' {
		return kindError(decoder, "message "+string(message.Descriptor().FullName()))
	}
	if _, err := decoder.ReadToken(); err != nil {
		return err
	}

	fields := message.Descriptor().Fields()
	for decoder.PeekKind() != '}' {
		name, err := decoder.ReadToken()
		if err != nil {
			return err
		}
		field := fields.ByName(protoreflect.Name(name.String()))
		if field == nil {
			if err := decoder.SkipValue(); err != nil {
				return err
			}
			continue
		}
		if err := decodeField(decoder, message, field); err != nil {
			return err
		}
	}
	_, err := decoder.ReadToken()
	return err
}

// decodeField reads the value of one field of the message.
//
// A null leaves the field unset, as it leaves a generated field at its zero
// value or nil.
func decodeField(
	decoder *dynamicDecoder,
	message protoreflect.Message,
	field protoreflect.FieldDescriptor,
) error {
	if decoder.PeekKind() == 'n' {
		_, err := decoder.ReadToken()
		return err
	}
	switch {
	case field.IsList():
		return decodeList(decoder, message.Mutable(field).List(), field)
	case field.IsMap():
		return decodeMap(decoder, message.Mutable(field).Map(), field)
	}

	if oneof := field.ContainingOneof(); oneof != nil && !oneof.IsSynthetic() {
		if set := message.WhichOneof(oneof); set != nil {
			return fmt.Errorf(
				"%s and %s of oneof %s are both set within %q",
				set.Name(), field.Name(), oneof.Name(), decoder.StackPointer(),
			)
		}
	}
	value, err := decodeValue(decoder, field, message.NewField(field))
	if err != nil {
		return err
	}
	message.Set(field, value)
	return nil
}

// decodeList reads a JSON array into the list of a repeated field.
func decodeList(
	decoder *dynamicDecoder,
	list protoreflect.List,
	field protoreflect.FieldDescriptor,
) error {
	if decoder.PeekKind() != '[' {
		return kindError(decoder, "a list")
	}
	if _, err := decoder.ReadToken(); err != nil {
		return err
	}
	for decoder.PeekKind() != ']' {
		value, err := decodeValue(decoder, field, list.NewElement())
		if err != nil {
			return err
		}
		list.Append(value)
	}
	_, err := decoder.ReadToken()
	return err
}

// decodeMap reads a JSON object into the map of a map field.
func decodeMap(
	decoder *dynamicDecoder,
	entries protoreflect.Map,
	field protoreflect.FieldDescriptor,
) error {
	if decoder.PeekKind() != '{' {
		return kindError(decoder, "a map")
	}
	if _, err := decoder.ReadToken(); err != nil {
		return err
	}
	for decoder.PeekKind() != '}' {
		name, err := decoder.ReadToken()
		if err != nil {
			return err
		}
		key, err := decodeMapKey(name.String(), field.MapKey())
		if err != nil {
			return fmt.Errorf("%w within %q", err, decoder.StackPointer())
		}
		value, err := decodeValue(decoder, field.MapValue(), entries.NewValue())
		if err != nil {
			return err
		}
		entries.Set(key, value)
	}
	_, err := decoder.ReadToken()
	return err
}

// decodeMapKey reads a map key from a JSON object name as encoding/json/v2
// reads it into the key's Go type: a string as it is, an integer as its
// JSON number.
func decodeMapKey(name string, key protoreflect.FieldDescriptor) (protoreflect.MapKey, error) {
	switch key.Kind() {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(name).MapKey(), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return decodeIntegerKey(name, protoreflect.ValueOfInt32)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return decodeIntegerKey(name, protoreflect.ValueOfInt64)
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return decodeIntegerKey(name, protoreflect.ValueOfUint32)
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return decodeIntegerKey(name, protoreflect.ValueOfUint64)
	}
	// encoding/json/v2 reads no bool from a JSON object name either.
	return protoreflect.MapKey{}, fmt.Errorf("a %s map key cannot be read from a JSON object name", key.Kind())
}

// decodeIntegerKey reads an integer map key as encoding/json/v2 reads the
// JSON number spelled by the name.
func decodeIntegerKey[T int32 | int64 | uint32 | uint64](
	name string,
	valueOf func(T) protoreflect.Value,
) (protoreflect.MapKey, error) {
	var key T
	if err := json.Unmarshal([]byte(name), &key); err != nil {
		return protoreflect.MapKey{}, fmt.Errorf("cannot read map key %q as %T: %w", name, key, err)
	}
	return valueOf(key).MapKey(), nil
}

// decodeValue reads one value of the field's kind: the field's own value, an
// element of its list or a value of its map.
//
// The fresh value is the message a message value is read into.
func decodeValue(
	decoder *dynamicDecoder,
	field protoreflect.FieldDescriptor,
	fresh protoreflect.Value,
) (protoreflect.Value, error) {
	switch field.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		if err := decodeMessageValue(decoder, fresh.Message()); err != nil {
			return protoreflect.Value{}, err
		}
		return fresh, nil
	case protoreflect.EnumKind:
		return decodeEnum(decoder, field.Enum())
	case protoreflect.BoolKind:
		return decodeScalar(decoder, protoreflect.ValueOfBool)
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return decodeScalar(decoder, protoreflect.ValueOfInt32)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return decodeScalar(decoder, protoreflect.ValueOfInt64)
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return decodeScalar(decoder, protoreflect.ValueOfUint32)
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return decodeScalar(decoder, protoreflect.ValueOfUint64)
	case protoreflect.FloatKind:
		return decodeScalar(decoder, protoreflect.ValueOfFloat32)
	case protoreflect.DoubleKind:
		return decodeScalar(decoder, protoreflect.ValueOfFloat64)
	case protoreflect.StringKind:
		return decodeScalar(decoder, protoreflect.ValueOfString)
	case protoreflect.BytesKind:
		return decodeScalar(decoder, protoreflect.ValueOfBytes)
	}
	return protoreflect.Value{}, fmt.Errorf("field %s has the unknown kind %s", field.FullName(), field.Kind())
}

// decodeScalar reads a scalar as encoding/json/v2 reads it into the Go type
// a generated message holds it in.
func decodeScalar[T any](
	decoder *dynamicDecoder,
	valueOf func(T) protoreflect.Value,
) (protoreflect.Value, error) {
	var value T
	if err := json.UnmarshalDecode(decoder.Decoder, &value); err != nil {
		return protoreflect.Value{}, err
	}
	return valueOf(value), nil
}

// decodeEnum reads an enum as its declared name or as a number, the rule of
// UnmarshalEnumJSON, and a null as the number zero.
func decodeEnum(
	decoder *dynamicDecoder,
	enum protoreflect.EnumDescriptor,
) (protoreflect.Value, error) {
	raw, err := decoder.ReadValue()
	if err != nil {
		return protoreflect.Value{}, err
	}
	if raw.Kind() == 'n' {
		return protoreflect.ValueOfEnum(0), nil
	}
	number, err := enumNumber(enum, raw)
	if err != nil {
		return protoreflect.Value{}, fmt.Errorf("%w within %q", err, decoder.StackPointer())
	}
	return protoreflect.ValueOfEnum(number), nil
}

// kindError consumes the value the decoder stands at and reports that it is
// not the wanted kind.
func kindError(decoder *dynamicDecoder, want string) error {
	raw, err := decoder.ReadValue()
	if err != nil {
		return err
	}
	return fmt.Errorf(
		"cannot read JSON %s as %s within %q", kindName(raw.Kind()), want, decoder.StackPointer(),
	)
}

// kindName names a JSON kind as the errors of encoding/json/v2 name it.
func kindName(kind jsontext.Kind) string {
	switch kind {
	case 'n':
		return "null"
	case 'f', 't':
		return "boolean"
	case '"':
		return "string"
	case '0':
		return "number"
	case '{':
		return "object"
	case '[':
		return "array"
	}
	return kind.String()
}
