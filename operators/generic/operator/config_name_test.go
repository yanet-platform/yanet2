package operator_test

import (
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const configNameMethod = "operators.generic.operator.testpb.ConfigNameService/UpdateConfig"

// Register once per process, including when tests repeat in the same binary.
func init() {
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("operators/generic/operator/config_name_test.proto"),
		Package: proto.String("operators.generic.operator.testpb"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Config"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:   proto.String("config_name"),
				Number: proto.Int32(1),
				Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			}},
		}},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("ConfigNameService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("UpdateConfig"),
				InputType:  proto.String(".operators.generic.operator.testpb.Config"),
				OutputType: proto.String(".operators.generic.operator.testpb.Config"),
			}},
		}},
	}, nil,
	)
	if err != nil {
		panic(err)
	}
	if err := protoregistry.GlobalFiles.RegisterFile(file); err != nil {
		panic(err)
	}
	messageType := configNameMessageType{dynamicpb.NewMessageType(file.Messages().ByName("Config"))}
	if err := protoregistry.GlobalTypes.RegisterMessage(messageType); err != nil {
		panic(err)
	}
}

// configNameMessageType preserves the adapter on fresh and read-only messages.
type configNameMessageType struct {
	protoreflect.MessageType
}

func (m configNameMessageType) New() protoreflect.Message {
	return &configNameMessage{m.MessageType.New()}
}

func (m configNameMessageType) Zero() protoreflect.Message {
	return &configNameMessage{m.MessageType.Zero()}
}

// configNameMessage adapts the dynamic fixture to ordinary JSON decoding,
// preserving the adapter through protobuf reflection and cloning.
type configNameMessage struct {
	protoreflect.Message
}

func (m *configNameMessage) ProtoReflect() protoreflect.Message {
	return m
}

func (m *configNameMessage) Interface() protoreflect.ProtoMessage {
	return m
}

func (m *configNameMessage) Type() protoreflect.MessageType {
	return configNameMessageType{m.Message.Type()}
}

func (m *configNameMessage) New() protoreflect.Message {
	return m.Type().New()
}

func (m *configNameMessage) UnmarshalJSON(data []byte) error {
	return protojson.Unmarshal(data, m.Message.Interface())
}
