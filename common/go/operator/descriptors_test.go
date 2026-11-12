package operator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// pipelineFile is the linked file of the pipeline service, whose update
// stands in for a module config.
func pipelineFile() *descriptorpb.FileDescriptorProto {
	return protodesc.ToFileDescriptorProto(
		(&ynpb.UpdatePipelineRequest{}).ProtoReflect().Descriptor().ParentFile(),
	)
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

// Test_LoadDescriptorSet_ResolvesImportsAmongLinkedFiles verifies that a set
// written without its imports resolves them among the linked files, and
// that its method takes a dynamic request of the set's type.
func Test_LoadDescriptorSet_ResolvesImportsAmongLinkedFiles(t *testing.T) {
	files, err := operator.LoadDescriptorSet(writeDescriptorSet(t, pipelineFile()))
	require.NoError(t, err)

	request, err := operator.NewMethodRequest(ynpb.PipelineService_Update_FullMethodName, files)
	require.NoError(t, err)
	dynamic, ok := request.(*dynamicpb.Message)
	require.True(t, ok, "got %T", request)
	require.Equal(
		t, protoreflect.FullName("controlplane.ynpb.v1.UpdatePipelineRequest"), dynamic.Descriptor().FullName(),
	)
}

// Test_LoadDescriptorSet_PrefersFilesOfTheSet verifies that an import the
// set holds is taken from the set, not from the linked files.
func Test_LoadDescriptorSet_PrefersFilesOfTheSet(t *testing.T) {
	pipeline := pipelineFile()
	linked := (&ynpb.UpdatePipelineRequest{}).ProtoReflect().Descriptor().ParentFile().Imports().Get(0)
	imported := protodesc.ToFileDescriptorProto(linked.FileDescriptor)
	imported.MessageType = append(imported.MessageType, &descriptorpb.DescriptorProto{Name: new("SetOnly")})

	files, err := operator.LoadDescriptorSet(writeDescriptorSet(t, pipeline, imported))
	require.NoError(t, err)

	_, err = files.FindDescriptorByName(protoreflect.FullName(imported.GetPackage() + ".SetOnly"))
	require.NoError(t, err)
}

// Test_LoadDescriptorSet_RefusesUnresolvedImport verifies that an import
// found neither in the set nor among the linked files is refused by name.
func Test_LoadDescriptorSet_RefusesUnresolvedImport(t *testing.T) {
	path := writeDescriptorSet(t, &descriptorpb.FileDescriptorProto{
		Name:       new("module.proto"),
		Syntax:     new("proto3"),
		Dependency: []string{"missing.proto"},
	})

	_, err := operator.LoadDescriptorSet(path)

	require.ErrorContains(t, err,
		`file "module.proto" imports "missing.proto", which is neither in the set nor linked into this binary`)
}

// Test_LoadDescriptorSet_RefusesUnreadableFile verifies that a missing file
// and a file that is not a descriptor set are refused with their path.
func Test_LoadDescriptorSet_RefusesUnreadableFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.protoset")
	_, err := operator.LoadDescriptorSet(missing)
	require.ErrorContains(t, err, "failed to read descriptor set")
	require.ErrorContains(t, err, missing)

	garbage := filepath.Join(t.TempDir(), "garbage.protoset")
	require.NoError(t, os.WriteFile(garbage, []byte("not a set"), 0o644))
	_, err = operator.LoadDescriptorSet(garbage)
	require.ErrorContains(t, err, "failed to parse descriptor set")
	require.ErrorContains(t, err, garbage)
}

// Test_NewMethodRequest_RejectsServiceOutsideTheSet verifies that a set
// without the spelled service refuses the method, although this binary
// links it.
func Test_NewMethodRequest_RejectsServiceOutsideTheSet(t *testing.T) {
	common := protodesc.ToFileDescriptorProto((&commonpb.PipelineId{}).ProtoReflect().Descriptor().ParentFile())
	files, err := operator.LoadDescriptorSet(writeDescriptorSet(t, common))
	require.NoError(t, err)

	_, err = operator.NewMethodRequest(ynpb.PipelineService_Update_FullMethodName, files)

	require.ErrorContains(t, err, `service "controlplane.ynpb.v1.PipelineService" is not in the descriptor set`)
}

// Test_StaticModuleOperator_PushesThroughDescriptorSet verifies that a
// target resolved in a set sends its dynamic request as given, its reply
// read into a dynamic message.
func Test_StaticModuleOperator_PushesThroughDescriptorSet(t *testing.T) {
	files, err := operator.LoadDescriptorSet(writeDescriptorSet(t, pipelineFile()))
	require.NoError(t, err)
	target := pipelineTarget("fn:one")
	want := target.Request
	request, err := operator.NewMethodRequest(target.Method, files)
	require.NoError(t, err)
	encoded, err := proto.Marshal(want)
	require.NoError(t, err)
	require.NoError(t, proto.Unmarshal(encoded, request))
	target.Descriptors = files
	target.Request = request

	gateway, endpoint := startFakeGateway(t)
	op, err := operator.NewStaticModuleOperator(
		"test", staticConfig(endpoint, freeEndpoint(t)), []operator.StaticTarget{target},
	)
	require.NoError(t, err)

	pipelines, _ := runUntilPushed(t, op, gateway)
	for _, pushed := range pipelines {
		require.True(t, proto.Equal(want, pushed), "got %v", pushed)
	}
}
