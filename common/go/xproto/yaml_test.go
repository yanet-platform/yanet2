package xproto_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/yanet-platform/yanet2/common/go/xproto"
)

// Test_Unmarshal_FieldsByProtoName verifies that keys spelled as proto
// field names fill a message, also where the JSON name is spelled apart.
func Test_Unmarshal_FieldsByProtoName(t *testing.T) {
	duration := &durationpb.Duration{}
	require.NoError(t, xproto.Unmarshal([]byte("seconds: 30\nnanos: 5\n"), duration))
	want := &durationpb.Duration{Seconds: 30, Nanos: 5}
	require.True(t, proto.Equal(want, duration), "got %v", duration)

	field := &descriptorpb.FieldDescriptorProto{}
	require.NoError(t, xproto.Unmarshal([]byte("json_name: x\n"), field))
	require.Equal(t, "x", field.GetJsonName())
}

// Test_Unmarshal_MessageWithOwnJSONForm verifies that a message carrying
// its own JSON decoder receives the document in that form, at the root too.
func Test_Unmarshal_MessageWithOwnJSONForm(t *testing.T) {
	input := "name: yanet\nratio: 1.5\nenabled: true\ntags: [a, b]\n"
	got := &structpb.Struct{}
	require.NoError(t, xproto.Unmarshal([]byte(input), got))

	want, err := structpb.NewStruct(map[string]any{
		"name":    "yanet",
		"ratio":   1.5,
		"enabled": true,
		"tags":    []any{"a", "b"},
	})
	require.NoError(t, err)
	require.True(t, proto.Equal(want, got), "got %v", got)

	scalar := &structpb.Value{}
	require.NoError(t, xproto.Unmarshal([]byte("42\n"), scalar))
	require.True(t, proto.Equal(structpb.NewNumberValue(42), scalar), "got %v", scalar)
}

// Test_Unmarshal_AnchorsAndMergeKeys verifies that an alias reuses an
// anchored node and a merge key fills the keys a mapping leaves unset.
func Test_Unmarshal_AnchorsAndMergeKeys(t *testing.T) {
	input := "base: &base {k: 1, j: 2}\nderived: {<<: *base, j: 3}\n"
	got := &structpb.Struct{}
	require.NoError(t, xproto.Unmarshal([]byte(input), got))

	derived := got.GetFields()["derived"].GetStructValue().AsMap()
	require.Equal(t, map[string]any{"k": 1.0, "j": 3.0}, derived)
}

// Test_Unmarshal_ResolvesTags verifies that a timestamp bound for a string
// field arrives in the parser's resolved form rather than as written.
func Test_Unmarshal_ResolvesTags(t *testing.T) {
	value := &wrapperspb.StringValue{}
	require.NoError(t, xproto.Unmarshal([]byte("value: 2024-01-01\n"), value))

	require.Equal(t, "2024-01-01T00:00:00Z", value.GetValue())
}

// Test_Unmarshal_KeepsLargeIntegersExact verifies that an integer written
// as an integer survives at the top of the 64-bit range.
func Test_Unmarshal_KeepsLargeIntegersExact(t *testing.T) {
	value := &wrapperspb.UInt64Value{}
	require.NoError(t, xproto.Unmarshal([]byte("value: 18446744073709551615\n"), value))

	require.Equal(t, uint64(18446744073709551615), value.GetValue())
}

// Test_Unmarshal_RejectsAliasBlowup verifies that the parser rejects a
// document whose aliases expand far beyond what it spells out.
func Test_Unmarshal_RejectsAliasBlowup(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("l0: &l0 [" + strings.Repeat("x, ", 10) + "]\n")
	for level := 1; level < 6; level++ {
		fmt.Fprintf(&builder, "l%d: &l%d [", level, level)
		for range 10 {
			fmt.Fprintf(&builder, "*l%d, ", level-1)
		}
		builder.WriteString("]\n")
	}

	err := xproto.Unmarshal([]byte(builder.String()), &structpb.Struct{})

	require.ErrorContains(t, err, "excessive aliasing")
}

// Test_Unmarshal_EmptyDocumentClearsMessage verifies that a stream without
// content clears the message, whatever the stream spells.
func Test_Unmarshal_EmptyDocumentClearsMessage(t *testing.T) {
	for _, input := range []string{"", "# nothing here\n", "---\n", "--- \n---\n"} {
		value := &wrapperspb.StringValue{Value: "stale"}
		require.NoError(t, xproto.Unmarshal([]byte(input), value), "%q", input)
		require.Empty(t, value.GetValue(), "%q", input)
	}
}

// Test_Unmarshal_TrailingSeparator verifies that a separator after the
// document, with or without a comment, is not a second document.
func Test_Unmarshal_TrailingSeparator(t *testing.T) {
	for _, input := range []string{"value: x\n---\n", "value: x\n---", "value: x\n---\n# end\n"} {
		value := &wrapperspb.StringValue{}
		require.NoError(t, xproto.Unmarshal([]byte(input), value), "%q", input)
		require.Equal(t, "x", value.GetValue(), "%q", input)
	}
}

// Test_Unmarshal_NullLeavesZeroValue verifies that a null leaves its field
// at the zero value instead of failing, as encoding/json treats null.
func Test_Unmarshal_NullLeavesZeroValue(t *testing.T) {
	value := &wrapperspb.StringValue{}
	require.NoError(t, xproto.Unmarshal([]byte("value: null\n"), value))

	require.Empty(t, value.GetValue())
}

// Test_Unmarshal_RejectedStreamLeavesMessage verifies that a stream
// rejected as a whole leaves the message as it was.
func Test_Unmarshal_RejectedStreamLeavesMessage(t *testing.T) {
	for _, input := range []string{"value: a\n---\nvalue: b\n", "value: .nan\n"} {
		value := &wrapperspb.StringValue{Value: "keep"}
		require.Error(t, xproto.Unmarshal([]byte(input), value), "%q", input)
		require.Equal(t, "keep", value.GetValue(), "%q", input)
	}
}

// Test_Unmarshal_RejectsNilTarget verifies that a nil target, typed or
// not, is an error rather than a crash.
func Test_Unmarshal_RejectsNilTarget(t *testing.T) {
	require.ErrorContains(t, xproto.Unmarshal([]byte("value: x\n"), nil), "nil")
	var typed *wrapperspb.StringValue
	require.ErrorContains(t, xproto.Unmarshal([]byte("value: x\n"), typed), "nil")
}

// Test_Unmarshal_RejectsMalformedDocuments verifies that each malformed
// document is rejected with the reason in the error.
func Test_Unmarshal_RejectsMalformedDocuments(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		message proto.Message
		wantErr string
	}{
		{
			name:    "unknown field",
			input:   "value: 1\nfoo: 2\n",
			message: &wrapperspb.Int32Value{},
			wantErr: `unknown field "foo"`,
		},
		{
			name:    "json name spelling",
			input:   "jsonName: x\n",
			message: &descriptorpb.FieldDescriptorProto{},
			wantErr: `unknown field "jsonName"`,
		},
		{
			name:    "duplicate key",
			input:   "value: a\nvalue: b\n",
			message: &wrapperspb.StringValue{},
			wantErr: `key "value" already defined`,
		},
		{
			name:    "second document in the stream",
			input:   "value: a\n---\nvalue: b\n",
			message: &wrapperspb.StringValue{},
			wantErr: "more than one document",
		},
		{
			name:    "document after empty ones",
			input:   "---\n---\nvalue: a\n",
			message: &wrapperspb.StringValue{},
			wantErr: "more than one document",
		},
		{
			name:    "explicit null as a second document",
			input:   "value: a\n---\nnull\n",
			message: &wrapperspb.StringValue{},
			wantErr: "more than one document",
		},
		{
			name:    "tagged empty null as a second document",
			input:   "value: a\n---\n!!null \"\"\n",
			message: &wrapperspb.StringValue{},
			wantErr: "more than one document",
		},
		{
			name:    "tagged null on the separator line",
			input:   "value: a\n--- !!null\n",
			message: &wrapperspb.StringValue{},
			wantErr: "more than one document",
		},
		{
			name:    "empty mapping as a second document",
			input:   "value: a\n---\n{}\n",
			message: &wrapperspb.StringValue{},
			wantErr: "more than one document",
		},
		{
			name:    "null list entry",
			input:   "message_type: [{name: a}, null]\n",
			message: &descriptorpb.FileDescriptorProto{},
			wantErr: "message_type[1] is null",
		},
		{
			name:    "null entry in a list of scalars",
			input:   "dependency: [a, null, b]\n",
			message: &descriptorpb.FileDescriptorProto{},
			wantErr: "dependency[1] is null",
		},
		{
			name:    "null list entry in a nested message",
			input:   "message_type: [{name: a, field: [null]}]\n",
			message: &descriptorpb.FileDescriptorProto{},
			wantErr: "message_type[0].field[0] is null",
		},
		{
			name:    "syntax error in the second document",
			input:   "value: a\n---\nvalue: [unclosed\n",
			message: &wrapperspb.StringValue{},
			wantErr: "yaml",
		},
		{
			name:    "non-string mapping key",
			input:   "443: allow\n",
			message: &wrapperspb.StringValue{},
			wantErr: "such as a non-string mapping key",
		},
		{
			name:    "value JSON cannot represent",
			input:   "value: .nan\n",
			message: &wrapperspb.StringValue{},
			wantErr: "such as a non-string mapping key or a NaN",
		},
		{
			name:    "alias cycle",
			input:   "a: &loop\n  b: *loop\n",
			message: &structpb.Struct{},
			wantErr: "contains itself",
		},
		{
			name:    "scalar for a message without its own JSON form",
			input:   "30s\n",
			message: &durationpb.Duration{},
			wantErr: "cannot unmarshal string",
		},
		{
			name:    "sequence where a scalar is expected",
			input:   "value: [1]\n",
			message: &wrapperspb.Int32Value{},
			wantErr: "cannot unmarshal array",
		},
		{
			name:    "mapping where a scalar is expected",
			input:   "seconds: {a: 1}\n",
			message: &durationpb.Duration{},
			wantErr: "cannot unmarshal object",
		},
		{
			name:    "unquoted number in a string field",
			input:   "value: 42\n",
			message: &wrapperspb.StringValue{},
			wantErr: "cannot unmarshal number",
		},
		{
			name:    "integer overflow",
			input:   "value: 4294967296\n",
			message: &wrapperspb.UInt32Value{},
			wantErr: "cannot unmarshal number 4294967296",
		},
		{
			name:    "negative unsigned",
			input:   "value: -1\n",
			message: &wrapperspb.UInt32Value{},
			wantErr: "cannot unmarshal number -1",
		},
		{
			name:    "syntax error",
			input:   "value: [unclosed\n",
			message: &wrapperspb.StringValue{},
			wantErr: "yaml",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := xproto.Unmarshal([]byte(tc.input), tc.message)

			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}
