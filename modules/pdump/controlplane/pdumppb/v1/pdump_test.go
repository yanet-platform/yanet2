package pdumppb_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	pdumppb "github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
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

// Test_SetConfigRequest_Validate verifies that the request-only mask rules
// report their exact field and rule text before configuration is applied.
func Test_SetConfigRequest_Validate(t *testing.T) {
	maxRingSizeMessage := ""
	if pdumppb.MaxRingSize&(pdumppb.MaxRingSize-1) != 0 {
		maxRingSizeMessage = fmt.Sprintf(
			"ring_size %d must be a power of two",
			pdumppb.MaxRingSize,
		)
	}
	normalMaxRingSizeMessage := ""
	if pdumppb.MaxRingSize < 1<<26 {
		normalMaxRingSizeMessage = fmt.Sprintf(
			"ring_size %d must be in range %d..%d",
			1<<26,
			1<<20,
			pdumppb.MaxRingSize,
		)
	}

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
			name:    "missing update mask",
			request: &pdumppb.SetConfigRequest{Name: "pdump0", Config: &pdumppb.Config{Filter: "udp"}},
			message: "update_mask is required",
		},
		{
			name: "update mask without paths",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{Filter: "udp"},
				UpdateMask: &pdumppb.FieldMask{},
			},
		},
		{
			name: "zero mode is accepted",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"mode"}},
			},
		},
		{
			name: "maximum mode is accepted",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{Mode: pdumppb.MaxMode},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"mode"}},
			},
		},
		{
			name: "mode above maximum",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{Mode: pdumppb.MaxMode + 1},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"mode"}},
			},
			message: "mode 4 must be in range 0..3",
		},
		{
			name: "zero snaplen",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"snaplen"}},
			},
			message: "snaplen must be greater than zero",
		},
		{
			name: "positive snaplen",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{Snaplen: 1},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"snaplen"}},
			},
		},
		{
			name: "ring size below minimum",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{RingSize: 1 << 19},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"ring_size"}},
			},
			message: fmt.Sprintf(
				"ring_size %d must be in range %d..%d",
				1<<19,
				1<<20,
				pdumppb.MaxRingSize,
			),
		},
		{
			name: "minimum ring size",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{RingSize: 1048576},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"ring_size"}},
			},
		},
		{
			name: "largest ASAN power-of-two ring size",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{RingSize: 1 << 25},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"ring_size"}},
			},
		},
		{
			name: "allocator maximum ring size",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{RingSize: pdumppb.MaxRingSize},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"ring_size"}},
			},
			message: maxRingSizeMessage,
		},
		{
			name: "normal allocator maximum ring size",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{RingSize: 1 << 26},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"ring_size"}},
			},
			message: normalMaxRingSizeMessage,
		},
		{
			name: "ring size above maximum",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{RingSize: 1 << 27},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"ring_size"}},
			},
			message: fmt.Sprintf(
				"ring_size %d must be in range %d..%d",
				1<<27,
				1<<20,
				pdumppb.MaxRingSize,
			),
		},
		{
			name: "unknown mask path",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"unknown"}},
			},
			message: "unknown path 'unknown'",
		},
		{
			name: "filter mask path",
			request: &pdumppb.SetConfigRequest{
				Name:       "pdump0",
				Config:     &pdumppb.Config{Filter: "tcp"},
				UpdateMask: &pdumppb.FieldMask{Paths: []string{"filter"}},
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
