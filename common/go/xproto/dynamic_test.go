package xproto_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xproto"
)

// sampleFile spells a module file that no generated type stands for, as a
// module's descriptor set carries it: linked address types, an enum, a
// oneof, maps and a message field of its own type.
const sampleFile = `
name: "xproto/sample.proto"
package: "xproto.sample"
syntax: "proto3"
dependency: "common/commonpb/v1/ipaddr.proto"
dependency: "common/commonpb/v1/ipprefix.proto"
enum_type {
  name: "Mode"
  value { name: "MODE_UNSPECIFIED" number: 0 }
  value { name: "MODE_FAST" number: 1 }
}
message_type {
  name: "Sample"
  field { name: "address" number: 1 label: LABEL_OPTIONAL type: TYPE_MESSAGE type_name: ".common.commonpb.v1.IPAddress" }
  field { name: "prefixes" number: 2 label: LABEL_REPEATED type: TYPE_MESSAGE type_name: ".common.commonpb.v1.IPPrefix" }
  field { name: "mode" number: 3 label: LABEL_OPTIONAL type: TYPE_ENUM type_name: ".xproto.sample.Mode" }
  field { name: "modes" number: 4 label: LABEL_REPEATED type: TYPE_ENUM type_name: ".xproto.sample.Mode" }
  field { name: "text" number: 5 label: LABEL_OPTIONAL type: TYPE_STRING oneof_index: 0 }
  field { name: "count" number: 6 label: LABEL_OPTIONAL type: TYPE_UINT32 oneof_index: 0 }
  field { name: "weights" number: 7 label: LABEL_REPEATED type: TYPE_MESSAGE type_name: ".xproto.sample.Sample.WeightsEntry" }
  field { name: "hosts" number: 8 label: LABEL_REPEATED type: TYPE_MESSAGE type_name: ".xproto.sample.Sample.HostsEntry" }
  field { name: "flags" number: 9 label: LABEL_REPEATED type: TYPE_MESSAGE type_name: ".xproto.sample.Sample.FlagsEntry" }
  field { name: "inner" number: 10 label: LABEL_OPTIONAL type: TYPE_MESSAGE type_name: ".xproto.sample.Sample" }
  nested_type {
    name: "WeightsEntry"
    options { map_entry: true }
    field { name: "key" number: 1 label: LABEL_OPTIONAL type: TYPE_STRING }
    field { name: "value" number: 2 label: LABEL_OPTIONAL type: TYPE_UINT32 }
  }
  nested_type {
    name: "HostsEntry"
    options { map_entry: true }
    field { name: "key" number: 1 label: LABEL_OPTIONAL type: TYPE_INT32 }
    field { name: "value" number: 2 label: LABEL_OPTIONAL type: TYPE_MESSAGE type_name: ".common.commonpb.v1.IPAddress" }
  }
  nested_type {
    name: "FlagsEntry"
    options { map_entry: true }
    field { name: "key" number: 1 label: LABEL_OPTIONAL type: TYPE_BOOL }
    field { name: "value" number: 2 label: LABEL_OPTIONAL type: TYPE_STRING }
  }
  oneof_decl { name: "choice" }
}
`

// newSample returns an empty dynamic message of the sample file, built from
// a full set that carries its own copies of the address files, as a set
// written with its imports does.
func newSample(t *testing.T, edits ...func(file *descriptorpb.FileDescriptorProto)) *dynamicpb.Message {
	t.Helper()
	sample := &descriptorpb.FileDescriptorProto{}
	require.NoError(t, prototext.Unmarshal([]byte(sampleFile), sample))
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{sample}}
	copied := map[string]bool{}
	var copyFile func(file protoreflect.FileDescriptor)
	copyFile = func(file protoreflect.FileDescriptor) {
		if copied[file.Path()] {
			return
		}
		copied[file.Path()] = true
		copiedFile := protodesc.ToFileDescriptorProto(file)
		for _, edit := range edits {
			edit(copiedFile)
		}
		set.File = append(set.File, copiedFile)
		imports := file.Imports()
		for idx := range imports.Len() {
			copyFile(imports.Get(idx).FileDescriptor)
		}
	}
	copyFile((&commonpb.IPAddress{}).ProtoReflect().Descriptor().ParentFile())
	copyFile((&commonpb.IPPrefix{}).ProtoReflect().Descriptor().ParentFile())
	files, err := protodesc.NewFiles(set)
	require.NoError(t, err)
	descriptor, err := files.FindDescriptorByName("xproto.sample.Sample")
	require.NoError(t, err)
	return dynamicpb.NewMessage(descriptor.(protoreflect.MessageDescriptor))
}

// requireLinkedForm checks that a value encodes as the linked type decodes
// the same document.
func requireLinkedForm(t *testing.T, want proto.Message, document string, got protoreflect.Value) {
	t.Helper()
	require.NoError(t, xproto.Unmarshal([]byte(document), want))
	require.Equal(t, deterministic(t, want), deterministic(t, got.Message().Interface()))
}

// unmarshalSample decodes the document into a fresh sample message.
func unmarshalSample(t *testing.T, document string) (*dynamicpb.Message, error) {
	t.Helper()
	message := newSample(t)
	return message, xproto.Unmarshal([]byte(document), message)
}

// field finds a field of the message by its name.
func field(message proto.Message, name string) protoreflect.FieldDescriptor {
	return message.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
}

// deterministic encodes the message in the order a byte comparison needs.
func deterministic(t *testing.T, message proto.Message) []byte {
	t.Helper()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	require.NoError(t, err)
	return encoded
}

// Test_Unmarshal_DynamicMessageMatchesGenerated verifies that a dynamic
// message of a linked type decodes to the bytes of the generated one, for
// scalars of every width, nulls, unknown keys at any depth, a type with a
// JSON form of its own and a message without fields.
func Test_Unmarshal_DynamicMessageMatchesGenerated(t *testing.T) {
	cases := []struct {
		name     string
		document string
		message  proto.Message
	}{
		{"signed integers", "seconds: -30\nnanos: 5\n", &durationpb.Duration{}},
		{"largest unsigned", "value: 18446744073709551615\n", &wrapperspb.UInt64Value{}},
		{"bytes", "value: AQI=\n", &wrapperspb.BytesValue{}},
		{"double", "value: 1.5\n", &wrapperspb.DoubleValue{}},
		{"float", "value: 0.25\n", &wrapperspb.FloatValue{}},
		{"bool", "value: true\n", &wrapperspb.BoolValue{}},
		{"null", "value: null\n", &wrapperspb.StringValue{}},
		{"nested with unknown keys", `
name: sample.proto
package: null
future: 1
message_type:
  - name: Sample
    future: {deep: [1]}
    field:
      - {name: id, number: 1, label: 1, type: 13, json_name: null}
options:
  java_package: x
  deprecated: false
`, &descriptorpb.FileDescriptorProto{}},
		{"own JSON form", "a: 1\nb: [x, true]\n", &structpb.Struct{}},
		{"fieldless", "name: x\nnested: {future: [1, 2]}\n", &emptypb.Empty{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.NoError(t, xproto.Unmarshal([]byte(c.document), c.message))
			dynamic := dynamicpb.NewMessage(c.message.ProtoReflect().Descriptor())
			require.NoError(t, xproto.Unmarshal([]byte(c.document), dynamic))

			require.Equal(t, deterministic(t, c.message), deterministic(t, dynamic))
		})
	}
}

// Test_Unmarshal_DynamicMessageKeepsLinkedJSONForms verifies that a field
// whose linked type has a JSON form of its own is read by that form, as a
// field, a list element and a map value, and that the message stays one
// that proto.Clone copies although the set carries its own copies of the
// linked files.
func Test_Unmarshal_DynamicMessageKeepsLinkedJSONForms(t *testing.T) {
	message, err := unmarshalSample(t, `
address: 198.51.100.7
prefixes: [10.0.0.0/8, "2001:db8::/32"]
hosts: {"443": "2001:db8::1"}
`)
	require.NoError(t, err)

	requireLinkedForm(t, &commonpb.IPAddress{}, "198.51.100.7", message.Get(field(message, "address")))
	prefixes := message.Get(field(message, "prefixes")).List()
	require.Equal(t, 2, prefixes.Len())
	requireLinkedForm(t, &commonpb.IPPrefix{}, "10.0.0.0/8", prefixes.Get(0))
	requireLinkedForm(t, &commonpb.IPPrefix{}, `"2001:db8::/32"`, prefixes.Get(1))
	host := message.Get(field(message, "hosts")).Map().Get(protoreflect.ValueOfInt32(443).MapKey())
	requireLinkedForm(t, &commonpb.IPAddress{}, `"2001:db8::1"`, host)

	require.True(t, proto.Equal(message, proto.Clone(message)))
}

// Test_Unmarshal_DynamicMessageRefusesJSONFormMissingFields verifies that a
// linked JSON form that does not read a field of the set's message, as an
// older binary's would not, refuses a value of that message, and that a
// document without such a value still decodes.
func Test_Unmarshal_DynamicMessageRefusesJSONFormMissingFields(t *testing.T) {
	withZone := func(file *descriptorpb.FileDescriptorProto) {
		if file.GetName() != "common/commonpb/v1/ipaddr.proto" {
			return
		}
		for _, message := range file.GetMessageType() {
			if message.GetName() == "IPAddress" {
				message.Field = append(message.Field, &descriptorpb.FieldDescriptorProto{
					Name:   new("zone"),
					Number: proto.Int32(15),
					Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				})
			}
		}
	}

	err := xproto.Unmarshal([]byte("mode: MODE_FAST\naddress: 198.51.100.7\n"), newSample(t, withZone))
	require.ErrorContains(t, err, "the JSON form of common.commonpb.v1.IPAddress in this binary "+
		`does not read field common.commonpb.v1.IPAddress.zone of the descriptor set within "/address"`)

	require.NoError(t, xproto.Unmarshal([]byte("mode: MODE_FAST\n"), newSample(t, withZone)))
}

// Test_Unmarshal_DynamicMessageReadsEnumsByNameOrNumber verifies that an
// enum takes its declared name or any int32 number, as UnmarshalEnumJSON
// does, and that an undeclared name is refused with the declared ones.
func Test_Unmarshal_DynamicMessageReadsEnumsByNameOrNumber(t *testing.T) {
	message, err := unmarshalSample(t, "mode: MODE_FAST\nmodes: [MODE_FAST, 0, 7]\n")
	require.NoError(t, err)

	require.Equal(t, protoreflect.EnumNumber(1), message.Get(field(message, "mode")).Enum())
	modes := message.Get(field(message, "modes")).List()
	require.Equal(t, 3, modes.Len())
	for idx, want := range []protoreflect.EnumNumber{1, 0, 7} {
		require.Equal(t, want, modes.Get(idx).Enum())
	}

	_, err = unmarshalSample(t, "mode: FAST\n")
	require.ErrorContains(t, err,
		`"FAST" is not a value of xproto.sample.Mode, want one of MODE_UNSPECIFIED, MODE_FAST within "/mode"`)
}

// Test_Unmarshal_DynamicMessageReadsOneofMember verifies that a oneof
// member is read by its field name, a zero value included, and that a
// second member of the same oneof is refused.
func Test_Unmarshal_DynamicMessageReadsOneofMember(t *testing.T) {
	message, err := unmarshalSample(t, "count: 0\n")
	require.NoError(t, err)
	choice := message.Descriptor().Oneofs().ByName("choice")
	require.Equal(t, protoreflect.Name("count"), message.WhichOneof(choice).Name())

	_, err = unmarshalSample(t, "text: a\ncount: 1\n")
	require.ErrorContains(t, err, `count and text of oneof choice are both set within "/text"`)
}

// Test_Unmarshal_DynamicMessageReadsMaps verifies that map keys follow
// encoding/json/v2: a string as it is, an integer as its JSON number, and
// no bool.
func Test_Unmarshal_DynamicMessageReadsMaps(t *testing.T) {
	message, err := unmarshalSample(t, "weights: {a: 1, b: null}\n")
	require.NoError(t, err)
	weights := message.Get(field(message, "weights")).Map()
	require.Equal(t, 2, weights.Len())
	require.Equal(t, uint64(1), weights.Get(protoreflect.ValueOfString("a").MapKey()).Uint())
	require.Equal(t, uint64(0), weights.Get(protoreflect.ValueOfString("b").MapKey()).Uint())

	_, err = unmarshalSample(t, `hosts: {"+1": 192.0.2.1}`+"\n")
	require.ErrorContains(t, err, `cannot read map key "+1" as int32`)
	require.ErrorContains(t, err, `within "/hosts/+1"`)

	_, err = unmarshalSample(t, `flags: {"true": x}`+"\n")
	require.ErrorContains(t, err, `a bool map key cannot be read from a JSON object name within "/flags/true"`)
}

// Test_Unmarshal_DynamicMessageRefusesWrongKinds verifies that a value of
// the wrong kind is refused with the pointer to it.
func Test_Unmarshal_DynamicMessageRefusesWrongKinds(t *testing.T) {
	cases := []struct {
		name     string
		document string
		wantErr  string
	}{
		{"scalar as message", "inner: x\n", `cannot read JSON string as message xproto.sample.Sample within "/inner"`},
		{"document as scalar", "5\n", `cannot read JSON number as message xproto.sample.Sample within ""`},
		{"scalar as list", "prefixes: 10.0.0.0/8\n", `cannot read JSON string as a list within "/prefixes"`},
		{"list as map", "weights: [1]\n", `cannot read JSON array as a map within "/weights"`},
		{"quoted number", `inner: {count: "5"}` + "\n", `within "/inner/count"`},
		{"map value", "weights: {a: x}\n", `within "/weights/a"`},
		{"list element", "modes: [MODE_FAST, {}]\n", `within "/modes/1"`},
		{"linked JSON form", "address: [1]\n", `within "/address"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := unmarshalSample(t, c.document)
			require.ErrorContains(t, err, c.wantErr)
		})
	}
}
