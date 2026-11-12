package operator_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	decappb "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
	dscppb "github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
	"github.com/yanet-platform/yanet2/operators/generic/operator"
)

const (
	decapUpdateMethod = "modules.decap.controlplane.decappb.v1.DecapService/UpdateConfig"
	dscpUpdateMethod  = "modules.dscp.controlplane.dscppb.v1.DscpService/UpdateConfig"
	// unlinkedUpdateMethod is the method of a module that only its
	// descriptor set describes.
	unlinkedUpdateMethod = "operators.generic.operator.unlinkedpb.UnlinkedService/UpdateConfig"
)

// unlinkedModule spells a module file that no binary links: its service,
// its request with an address and its reply exist only in a descriptor set.
func unlinkedModule() *descriptorpb.FileDescriptorProto {
	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	return &descriptorpb.FileDescriptorProto{
		Name:       new("operators/generic/operator/unlinked_test.proto"),
		Package:    new("operators.generic.operator.unlinkedpb"),
		Syntax:     new("proto3"),
		Dependency: []string{"common/commonpb/v1/ipaddr.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: new("UpdateConfigRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   new("name"),
						Number: proto.Int32(1),
						Label:  optional,
						Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					},
					{
						Name:     new("source"),
						Number:   proto.Int32(2),
						Label:    optional,
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: new(".common.commonpb.v1.IPAddress"),
					},
					{
						Name:   new("hop_limit"),
						Number: proto.Int32(3),
						Label:  optional,
						Type:   descriptorpb.FieldDescriptorProto_TYPE_UINT32.Enum(),
					},
				},
			},
			{Name: new("UpdateConfigResponse")},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: new("UnlinkedService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       new("UpdateConfig"),
				InputType:  new(".operators.generic.operator.unlinkedpb.UpdateConfigRequest"),
				OutputType: new(".operators.generic.operator.unlinkedpb.UpdateConfigResponse"),
			}},
		}},
	}
}

// writeDescriptorSet stores the files as one descriptor set in a fresh
// directory and returns its path.
func writeDescriptorSet(t *testing.T, files ...*descriptorpb.FileDescriptorProto) string {
	t.Helper()
	encoded, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: files})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "module.protoset")
	require.NoError(t, os.WriteFile(path, encoded, 0o644))
	return path
}

// loadDescriptorSet stores the files as one descriptor set and loads it.
func loadDescriptorSet(t *testing.T, files ...*descriptorpb.FileDescriptorProto) *protoregistry.Files {
	t.Helper()
	loaded, err := commonoperator.LoadDescriptorSet(writeDescriptorSet(t, files...))
	require.NoError(t, err)
	return loaded
}

// deterministic encodes the message in the order a byte comparison needs.
func deterministic(t *testing.T, message proto.Message) []byte {
	t.Helper()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	require.NoError(t, err)
	return encoded
}

// writeModuleConfig stores a module config file in a fresh directory and
// returns its path.
func writeModuleConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
	return path
}

// Test_LoadRequest_DecodesSpelledMethodRequest verifies that the file
// decodes into the request type the spelled method takes.
func Test_LoadRequest_DecodesSpelledMethodRequest(t *testing.T) {
	path := writeModuleConfig(t, `
name: decap0
prefixes4:
  - 10.0.0.0/8
`)

	request, err := operator.LoadRequest(decapUpdateMethod, path, nil)
	require.NoError(t, err)

	decap, ok := request.(*decappb.UpdateConfigRequest)
	require.True(t, ok)
	require.Equal(t, "decap0", decap.GetName())
	require.Len(t, decap.GetPrefixes4(), 1)
	require.Equal(t, "10.0.0.0/8", decap.GetPrefixes4()[0].AsLogValue())
}

// Test_LoadRequest_IgnoresUnknownKeys verifies that newer module fields
// do not prevent an operator from publishing the fields it understands.
func Test_LoadRequest_IgnoresUnknownKeys(t *testing.T) {
	path := writeModuleConfig(t, `
name: dscp0
future: true
config:
  prefixes4: [192.0.2.0/24]
  future: {prefixes: [198.51.100.0/24]}
  dscp_config: {flag: 2, mark: 46, future: null}
`)
	request, err := operator.LoadRequest(dscpUpdateMethod, path, nil)
	require.NoError(t, err)
	dscp := request.(*dscppb.UpdateConfigRequest)
	require.Equal(t, "dscp0", dscp.GetName())
	require.Len(t, dscp.GetConfig().GetDestinationPrefixes4(), 1)
	require.Equal(t, "192.0.2.0/24", dscp.GetConfig().GetDestinationPrefixes4()[0].AsLogValue())
	require.Equal(t, uint32(2), dscp.GetConfig().GetDscpConfig().GetFlag())
	require.Equal(t, uint32(46), dscp.GetConfig().GetDscpConfig().GetMark())
}

// Test_LoadRequest_DescriptorSetMatchesLinkedTypes verifies that a request
// decoded through its module's descriptor set equals, byte for byte, the
// one the linked types decode, with address types, a config with a JSON
// form of its own and unknown keys at every depth.
func Test_LoadRequest_DescriptorSetMatchesLinkedTypes(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		request  proto.Message
		document string
	}{
		{
			name:     "decap",
			method:   decapUpdateMethod,
			request:  &decappb.UpdateConfigRequest{},
			document: "name: decap0\nfuture: 1\nprefixes4: [10.0.0.0/8]\nprefixes6: [2001:db8::/32]\n",
		},
		{
			name:    "dscp",
			method:  dscpUpdateMethod,
			request: &dscppb.UpdateConfigRequest{},
			document: `
name: dscp0
future: true
config:
  prefixes4: [192.0.2.0/24]
  future: {prefixes: [198.51.100.0/24]}
  dscp_config: {flag: 2, mark: 46, future: null}
`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeModuleConfig(t, c.document)
			module := protodesc.ToFileDescriptorProto(c.request.ProtoReflect().Descriptor().ParentFile())

			linked, err := operator.LoadRequest(c.method, path, nil)
			require.NoError(t, err)
			described, err := operator.LoadRequest(c.method, path, loadDescriptorSet(t, module))
			require.NoError(t, err)

			_, ok := described.(*dynamicpb.Message)
			require.True(t, ok, "got %T", described)
			require.Equal(t, deterministic(t, linked), deterministic(t, described))
		})
	}
}

// Test_LoadRequest_DecodesRequestOfUnlinkedModule verifies that a module
// this binary does not link is decoded through its descriptor set, its
// address field read by the linked address type.
func Test_LoadRequest_DecodesRequestOfUnlinkedModule(t *testing.T) {
	path := writeModuleConfig(t, "name: tunnel0\nsource: 2001:db8::1\nhop_limit: 64\n")

	request, err := operator.LoadRequest(unlinkedUpdateMethod, path, loadDescriptorSet(t, unlinkedModule()))
	require.NoError(t, err)

	message := request.ProtoReflect()
	fields := message.Descriptor().Fields()
	require.Equal(t, "tunnel0", message.Get(fields.ByName("name")).String())
	require.Equal(t, uint64(64), message.Get(fields.ByName("hop_limit")).Uint())
	source := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("2001:db8::1"))
	require.Equal(t, deterministic(t, source), deterministic(t, message.Get(fields.ByName("source")).Message().Interface()))
}

// Test_LoadRequest_RefusesWrongKindThroughDescriptorSet verifies that a
// value of the wrong kind is refused with the file and the pointer to it.
func Test_LoadRequest_RefusesWrongKindThroughDescriptorSet(t *testing.T) {
	path := writeModuleConfig(t, "hop_limit: [64]\n")

	_, err := operator.LoadRequest(unlinkedUpdateMethod, path, loadDescriptorSet(t, unlinkedModule()))

	require.ErrorContains(t, err, path)
	require.ErrorContains(t, err, `within "/hop_limit"`)
}

// Test_LoadRequest_RefusesJSONFormOlderThanTheSet verifies that a set whose
// config has a field the linked JSON form does not read, as a newer
// module's set has for an older binary, is refused with the field named
// rather than published without it.
func Test_LoadRequest_RefusesJSONFormOlderThanTheSet(t *testing.T) {
	module := protodesc.ToFileDescriptorProto((&dscppb.UpdateConfigRequest{}).ProtoReflect().Descriptor().ParentFile())
	for _, message := range module.GetMessageType() {
		if message.GetName() == "Config" {
			message.Field = append(message.Field, &descriptorpb.FieldDescriptorProto{
				Name:   new("future_prefixes"),
				Number: proto.Int32(99),
				Label:  descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
				Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			})
		}
	}
	path := writeModuleConfig(t, "name: dscp0\nconfig:\n  prefixes4: [192.0.2.0/24]\n")

	_, err := operator.LoadRequest(dscpUpdateMethod, path, loadDescriptorSet(t, module))

	require.ErrorContains(t, err, "the JSON form of modules.dscp.controlplane.dscppb.v1.Config in this binary "+
		"does not read field modules.dscp.controlplane.dscppb.v1.Config.future_prefixes of the descriptor set")
	require.ErrorContains(t, err, `within "/config"`)
}

// Test_LoadRequest_ComposesIncludes verifies that module requests retain
// included payloads while the declaring file supplies the object name.
func Test_LoadRequest_ComposesIncludes(t *testing.T) {
	path := writeModuleConfig(t, "include: [payload.yaml]\nname: decap0\n")
	fragment := filepath.Join(filepath.Dir(path), "payload.yaml")
	require.NoError(t, os.WriteFile(fragment, []byte("prefixes4: [10.0.0.0/8]\n"), 0o600))
	request, err := operator.LoadRequest(decapUpdateMethod, path)
	require.NoError(t, err)
	decap := request.(*decappb.UpdateConfigRequest)
	require.Equal(t, "decap0", decap.GetName())
	require.Len(t, decap.GetPrefixes4(), 1)
	require.Equal(t, "10.0.0.0/8", decap.GetPrefixes4()[0].AsLogValue())
}
