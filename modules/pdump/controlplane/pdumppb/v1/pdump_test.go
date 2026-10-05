package pdumppb_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	pdumppb "github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
	ringpb "github.com/yanet-platform/yanet2/objects/ring/controlplane/ringpb/v1"
)

// Test_ShowConfigRequest_Validate verifies that an empty or nil request is
// rejected while a named request passes.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *pdumppb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &pdumppb.ShowConfigRequest{}, message: "name is required"},
		{name: "name set", request: &pdumppb.ShowConfigRequest{Name: "pdump0"}},
		{name: "nil request", request: nil, message: "name is required"},
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

// Test_SetConfigRequest_Validate verifies that the rules for the config fields
// a request carries report their exact field and rule text.
func Test_SetConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *pdumppb.SetConfigRequest
		message string
	}{
		{
			name:    "empty name",
			request: &pdumppb.SetConfigRequest{Config: &pdumppb.Config{}},
			message: "name is required",
		},
		{name: "nil request", request: nil, message: "name is required"},
		{
			name:    "missing config",
			request: &pdumppb.SetConfigRequest{Name: "pdump0"},
			message: "config is required",
		},
		{
			name:    "config without fields",
			request: &pdumppb.SetConfigRequest{Name: "pdump0", Config: &pdumppb.Config{}},
		},
		{
			name: "absent snaplen is not checked",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{Filter: proto.String("tcp")},
			},
		},
		{
			name: "zero mode is accepted",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{Mode: proto.Uint32(0)},
			},
		},
		{
			name: "maximum mode is accepted",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{Mode: proto.Uint32(pdumppb.MaxMode)},
			},
		},
		{
			name: "mode above maximum",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{Mode: proto.Uint32(pdumppb.MaxMode + 1)},
			},
			message: "config: mode 4 must be in range 0..3",
		},
		{
			name: "zero snaplen",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{Snaplen: proto.Uint32(0)},
			},
			message: "config: snaplen must be greater than zero",
		},
		{
			name: "positive snaplen",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{Snaplen: proto.Uint32(1)},
			},
		},
		{
			name: "ring_name is absent",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{Snaplen: proto.Uint32(1)},
			},
		},
		{
			name: "ring_name is empty",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{RingName: proto.String("")},
			},
			message: "config: ring_name is required",
		},
		{
			name: "ring_name contains NUL",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{RingName: proto.String("ring\x00a")},
			},
			message: "config: ring_name must not contain NUL",
		},
		{
			name: "ring_name is overlong",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{RingName: proto.String(strings.Repeat("r", ringpb.MaxRingNameLen))},
			},
			message: fmt.Sprintf("config: ring_name must be shorter than %d bytes", ringpb.MaxRingNameLen),
		},
		{
			name: "ring_name at the longest accepted length",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{RingName: proto.String(strings.Repeat("r", ringpb.MaxRingNameLen-1))},
			},
		},
		{
			name: "ring_name is valid",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{RingName: proto.String("ring0")},
			},
		},
		{
			name: "filter contains NUL",
			request: &pdumppb.SetConfigRequest{
				Name:   "pdump0",
				Config: &pdumppb.Config{Filter: proto.String("tcp\x00udp")},
			},
			message: "config: filter must not contain NUL",
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

// Test_DeleteConfigRequest_Validate verifies that an empty or nil request is
// rejected while a named request passes.
func Test_DeleteConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *pdumppb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &pdumppb.DeleteConfigRequest{}, message: "name is required"},
		{name: "name set", request: &pdumppb.DeleteConfigRequest{Name: "pdump0"}},
		{name: "nil request", request: nil, message: "name is required"},
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

// Test_ReadDumpRequest_Validate verifies that the streaming request applies
// the same required-name rule as the unary configuration requests.
func Test_ReadDumpRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *pdumppb.ReadDumpRequest
		message string
	}{
		{name: "empty name", request: &pdumppb.ReadDumpRequest{}, message: "name is required"},
		{name: "name set", request: &pdumppb.ReadDumpRequest{Name: "pdump0"}},
		{name: "nil request", request: nil, message: "name is required"},
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
