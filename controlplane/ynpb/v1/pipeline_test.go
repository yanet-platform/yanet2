package ynpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Test_Pipeline_Validate verifies that the pipeline identity and repeated
// function identifiers are required.
func Test_Pipeline_Validate(t *testing.T) {
	cases := []struct {
		name     string
		pipeline *ynpb.Pipeline
		message  string
	}{
		{name: "nil pipeline", pipeline: nil, message: "pipeline is required"},
		{name: "missing id", pipeline: &ynpb.Pipeline{}, message: "id is required"},
		{
			name:     "missing id name",
			pipeline: &ynpb.Pipeline{Id: &commonpb.PipelineId{}},
			message:  "id.name is required",
		},
		{
			name: "nil function identifier at repeated index",
			pipeline: &ynpb.Pipeline{
				Id:        &commonpb.PipelineId{Name: "pipeline0"},
				Functions: []*commonpb.FunctionId{{Name: "function0"}, nil},
			},
			message: "functions[1] is required",
		},
		{
			name: "valid pipeline",
			pipeline: &ynpb.Pipeline{
				Id:        &commonpb.PipelineId{Name: "pipeline0"},
				Functions: []*commonpb.FunctionId{{Name: "function0"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.pipeline.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_GetPipelineRequest_Validate verifies that both pipeline identifier
// presence and its name are checked before a state lookup.
//
// The name obeys the fixed-size buffer and NUL rules.
func Test_GetPipelineRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *ynpb.GetPipelineRequest
		message string
	}{
		{name: "nil request", request: nil, message: "id is required"},
		{name: "missing id", request: &ynpb.GetPipelineRequest{}, message: "id is required"},
		{
			name:    "missing id name",
			request: &ynpb.GetPipelineRequest{Id: &commonpb.PipelineId{}},
			message: "id.name is required",
		},
		{
			name:    "named id",
			request: &ynpb.GetPipelineRequest{Id: &commonpb.PipelineId{Name: "pipeline0"}},
		},
		{
			name:    "id name with NUL",
			request: &ynpb.GetPipelineRequest{Id: &commonpb.PipelineId{Name: "pipeline0\x00other"}},
			message: "id.name must not contain NUL",
		},
		{
			name: "id name of the buffer size",
			request: &ynpb.GetPipelineRequest{
				Id: &commonpb.PipelineId{Name: strings.Repeat("p", commonpb.MaxPipelineNameLen)},
			},
			message: "id.name must be shorter than 80 bytes",
		},
		{
			name: "longest id name",
			request: &ynpb.GetPipelineRequest{
				Id: &commonpb.PipelineId{Name: strings.Repeat("p", commonpb.MaxPipelineNameLen-1)},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_UpdatePipelineRequest_Validate verifies that the update delegates
// nested failures with the pipeline field path.
func Test_UpdatePipelineRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *ynpb.UpdatePipelineRequest
		message string
	}{
		{name: "nil request", request: nil, message: "pipeline is required"},
		{name: "missing pipeline", request: &ynpb.UpdatePipelineRequest{}, message: "pipeline is required"},
		{
			name:    "missing pipeline id",
			request: &ynpb.UpdatePipelineRequest{Pipeline: &ynpb.Pipeline{}},
			message: "pipeline: id is required",
		},
		{
			name: "missing pipeline id name",
			request: &ynpb.UpdatePipelineRequest{Pipeline: &ynpb.Pipeline{
				Id: &commonpb.PipelineId{},
			}},
			message: "pipeline: id.name is required",
		},
		{
			name: "nil function identifier",
			request: &ynpb.UpdatePipelineRequest{Pipeline: &ynpb.Pipeline{
				Id:        &commonpb.PipelineId{Name: "pipeline0"},
				Functions: []*commonpb.FunctionId{nil},
			}},
			message: "pipeline: functions[0] is required",
		},
		{
			name: "valid request",
			request: &ynpb.UpdatePipelineRequest{Pipeline: &ynpb.Pipeline{
				Id: &commonpb.PipelineId{Name: "pipeline0"},
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_DeletePipelineRequest_Validate verifies that deletion uses the same
// identifier rules as get and update.
func Test_DeletePipelineRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *ynpb.DeletePipelineRequest
		message string
	}{
		{name: "nil request", request: nil, message: "id is required"},
		{name: "missing id", request: &ynpb.DeletePipelineRequest{}, message: "id is required"},
		{
			name:    "missing id name",
			request: &ynpb.DeletePipelineRequest{Id: &commonpb.PipelineId{}},
			message: "id.name is required",
		},
		{
			name:    "named id",
			request: &ynpb.DeletePipelineRequest{Id: &commonpb.PipelineId{Name: "pipeline0"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}
