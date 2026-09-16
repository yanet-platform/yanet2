package unrduppb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	unrduppb "github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

// Test_ShowConfigRequest_Validate verifies that every invalid config name is
// rejected while names within the C buffer's usable limit pass.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *unrduppb.ShowConfigRequest
		message string
	}{
		{
			name:    "empty name",
			request: &unrduppb.ShowConfigRequest{},
			message: "name is required",
		},
		{
			name:    "embedded NUL",
			request: &unrduppb.ShowConfigRequest{Name: "unrdup\x000"},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &unrduppb.ShowConfigRequest{
				Name: strings.Repeat("u", commonpb.MaxModuleNameLen-1),
			},
		},
		{
			name: "name exceeds usable limit",
			request: &unrduppb.ShowConfigRequest{
				Name: strings.Repeat("u", commonpb.MaxModuleNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "nil request",
			message: "name is required",
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

// Test_UpdateConfigRequest_Validate verifies that names, the required config,
// and nested service rules are reported at their protobuf field paths.
func Test_UpdateConfigRequest_Validate(t *testing.T) {
	validService := &unrduppb.Service{
		Peers: []*commonpb.IPAddress{{}},
		Endpoints: []*unrduppb.Endpoint{{
			Port:     443,
			Protocol: unrduppb.Protocol_PROTOCOL_TCP,
		}},
	}

	cases := []struct {
		name    string
		request *unrduppb.UpdateConfigRequest
		message string
	}{
		{
			name:    "empty name",
			request: &unrduppb.UpdateConfigRequest{},
			message: "name is required",
		},
		{
			name:    "embedded NUL",
			request: &unrduppb.UpdateConfigRequest{Name: "unrdup\x000"},
			message: "name must not contain NUL",
		},
		{
			name: "name exceeds usable limit",
			request: &unrduppb.UpdateConfigRequest{
				Name: strings.Repeat("u", commonpb.MaxModuleNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "missing config",
			request: &unrduppb.UpdateConfigRequest{Name: "unrdup0"},
			message: "config is required",
		},
		{
			name: "missing peers",
			request: &unrduppb.UpdateConfigRequest{
				Name: "unrdup0",
				Config: &unrduppb.Config{Services: []*unrduppb.Service{{
					Endpoints: validService.Endpoints,
				}}},
			},
			message: "config: services[0]: peers must contain at least one peer",
		},
		{
			name: "missing endpoints",
			request: &unrduppb.UpdateConfigRequest{
				Name: "unrdup0",
				Config: &unrduppb.Config{Services: []*unrduppb.Service{{
					Peers: validService.Peers,
				}}},
			},
			message: "config: services[0]: endpoints must contain at least one endpoint",
		},
		{
			name: "port below range",
			request: &unrduppb.UpdateConfigRequest{
				Name: "unrdup0",
				Config: &unrduppb.Config{Services: []*unrduppb.Service{{
					Peers: []*commonpb.IPAddress{{}},
					Endpoints: []*unrduppb.Endpoint{{
						Protocol: unrduppb.Protocol_PROTOCOL_TCP,
					}},
				}}},
			},
			message: "config: services[0]: endpoints[0]: port 0 must be in range 1..65535",
		},
		{
			name: "protocol is unspecified",
			request: &unrduppb.UpdateConfigRequest{
				Name: "unrdup0",
				Config: &unrduppb.Config{Services: []*unrduppb.Service{{
					Peers:     []*commonpb.IPAddress{{}},
					Endpoints: []*unrduppb.Endpoint{{Port: 443}},
				}}},
			},
			message: "config: services[0]: endpoints[0]: protocol unknown value 0",
		},
		{
			name:    "valid empty service list",
			request: &unrduppb.UpdateConfigRequest{Name: "unrdup0", Config: &unrduppb.Config{}},
		},
		{
			name: "valid service",
			request: &unrduppb.UpdateConfigRequest{
				Name:   "unrdup0",
				Config: &unrduppb.Config{Services: []*unrduppb.Service{validService}},
			},
		},
		{
			name:    "nil request",
			message: "name is required",
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

// Test_DeleteConfigRequest_Validate verifies that Show and Delete apply the
// same complete config-name rule as Update.
func Test_DeleteConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *unrduppb.DeleteConfigRequest
		message string
	}{
		{
			name:    "empty name",
			request: &unrduppb.DeleteConfigRequest{},
			message: "name is required",
		},
		{
			name:    "embedded NUL",
			request: &unrduppb.DeleteConfigRequest{Name: "unrdup\x000"},
			message: "name must not contain NUL",
		},
		{
			name: "name exceeds usable limit",
			request: &unrduppb.DeleteConfigRequest{
				Name: strings.Repeat("u", commonpb.MaxModuleNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "valid name",
			request: &unrduppb.DeleteConfigRequest{Name: "unrdup0"},
		},
		{
			name:    "nil request",
			message: "name is required",
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

// Test_Config_Validate verifies that nested service errors retain their
// repeated field index while an empty service list passes.
func Test_Config_Validate(t *testing.T) {
	cases := []struct {
		name    string
		config  *unrduppb.Config
		message string
	}{
		{
			name:   "empty service list",
			config: &unrduppb.Config{},
		},
		{
			name: "invalid service at repeated index",
			config: &unrduppb.Config{
				Services: []*unrduppb.Service{
					{
						Peers: []*commonpb.IPAddress{{}},
						Endpoints: []*unrduppb.Endpoint{{
							Port:     443,
							Protocol: unrduppb.Protocol_PROTOCOL_TCP,
						}},
					},
					{},
				},
			},
			message: "services[1]: peers must contain at least one peer",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_Service_Validate verifies that peer and endpoint counts are required
// and that endpoint errors carry their repeated field path.
func Test_Service_Validate(t *testing.T) {
	cases := []struct {
		name    string
		service *unrduppb.Service
		message string
	}{
		{
			name: "missing peers",
			service: &unrduppb.Service{
				Endpoints: []*unrduppb.Endpoint{{
					Port:     443,
					Protocol: unrduppb.Protocol_PROTOCOL_TCP,
				}},
			},
			message: "peers must contain at least one peer",
		},
		{
			name: "missing endpoints",
			service: &unrduppb.Service{
				Peers: []*commonpb.IPAddress{{}},
			},
			message: "endpoints must contain at least one endpoint",
		},
		{
			name: "invalid endpoint at repeated index",
			service: &unrduppb.Service{
				Peers: []*commonpb.IPAddress{{}},
				Endpoints: []*unrduppb.Endpoint{
					{Port: 443, Protocol: unrduppb.Protocol_PROTOCOL_TCP},
					{Port: 443},
				},
			},
			message: "endpoints[1]: protocol unknown value 0",
		},
		{
			name: "valid service",
			service: &unrduppb.Service{
				Peers: []*commonpb.IPAddress{{}},
				Endpoints: []*unrduppb.Endpoint{{
					Port:     443,
					Protocol: unrduppb.Protocol_PROTOCOL_UDP,
				}},
			},
		},
		{
			name:    "nil service",
			message: "peers must contain at least one peer",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.service.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_Endpoint_Validate verifies that only nonzero 16-bit ports and served
// protocol enum values are accepted.
func Test_Endpoint_Validate(t *testing.T) {
	cases := []struct {
		name     string
		endpoint *unrduppb.Endpoint
		message  string
	}{
		{
			name:     "port zero",
			endpoint: &unrduppb.Endpoint{Protocol: unrduppb.Protocol_PROTOCOL_TCP},
			message:  "port 0 must be in range 1..65535",
		},
		{
			name:     "port at maximum",
			endpoint: &unrduppb.Endpoint{Port: 65535, Protocol: unrduppb.Protocol_PROTOCOL_TCP},
		},
		{
			name:     "port above maximum",
			endpoint: &unrduppb.Endpoint{Port: 65536, Protocol: unrduppb.Protocol_PROTOCOL_TCP},
			message:  "port 65536 must be in range 1..65535",
		},
		{
			name:     "unspecified protocol",
			endpoint: &unrduppb.Endpoint{Port: 443},
			message:  "protocol unknown value 0",
		},
		{
			name:     "unknown protocol value",
			endpoint: &unrduppb.Endpoint{Port: 443, Protocol: 3},
			message:  "protocol unknown value 3",
		},
		{
			name:     "TCP protocol",
			endpoint: &unrduppb.Endpoint{Port: 443, Protocol: unrduppb.Protocol_PROTOCOL_TCP},
		},
		{
			name:     "UDP protocol",
			endpoint: &unrduppb.Endpoint{Port: 443, Protocol: unrduppb.Protocol_PROTOCOL_UDP},
		},
		{
			name:    "nil endpoint",
			message: "port 0 must be in range 1..65535",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.endpoint.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}
