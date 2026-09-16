package l3bpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
)

// Test_CreateServiceRequest_Validate verifies that the required service,
// service name, and source filter messages are validated before conversion.
func Test_CreateServiceRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *l3bpb.CreateServiceRequest
		message string
	}{
		{
			name:    "missing service",
			request: &l3bpb.CreateServiceRequest{},
			message: "service is required",
		},
		{
			name: "empty nested name",
			request: &l3bpb.CreateServiceRequest{
				Service: &l3bpb.VirtualService{},
			},
			message: "service: name is required",
		},
		{
			name: "overlong nested name",
			request: &l3bpb.CreateServiceRequest{
				Service: &l3bpb.VirtualService{Name: strings.Repeat("a", 80)},
			},
			message: "service: name must be at most 79 bytes",
		},
		{
			name: "control byte in nested name",
			request: &l3bpb.CreateServiceRequest{
				Service: &l3bpb.VirtualService{Name: "vs\x00"},
			},
			message: "service: name must contain only printable bytes",
		},
		{
			name: "nil network at repeated index",
			request: &l3bpb.CreateServiceRequest{
				Service: &l3bpb.VirtualService{
					Name: "vs0",
					SourceFilterRules: []*l3bpb.SourceFilterRule{{
						Net6S: []*filterpb.IPNet{nil},
					}},
				},
			},
			message: "service: source_filter_rules[0]: net6s[0] is required",
		},
		{
			name: "named service",
			request: &l3bpb.CreateServiceRequest{
				Service: &l3bpb.VirtualService{Name: "vs0"},
			},
		},
		{name: "nil request", request: nil, message: "service is required"},
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

// Test_UpdateServiceRequest_Validate verifies that service validation is
// delegated with the nested field path preserved.
func Test_UpdateServiceRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *l3bpb.UpdateServiceRequest
		message string
	}{
		{name: "missing service", request: &l3bpb.UpdateServiceRequest{}, message: "service is required"},
		{
			name: "empty nested name",
			request: &l3bpb.UpdateServiceRequest{
				Service: &l3bpb.VirtualService{},
			},
			message: "service: name is required",
		},
		{
			name: "named service",
			request: &l3bpb.UpdateServiceRequest{
				Service: &l3bpb.VirtualService{Name: "vs0"},
			},
		},
		{name: "nil request", request: nil, message: "service is required"},
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

// Test_DeleteServiceRequest_Validate verifies that the service name uses the
// fixed length and printable-byte rules.
func Test_DeleteServiceRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *l3bpb.DeleteServiceRequest
		message string
	}{
		{name: "empty name", request: &l3bpb.DeleteServiceRequest{}, message: "name is required"},
		{
			name:    "overlong name",
			request: &l3bpb.DeleteServiceRequest{Name: strings.Repeat("a", 80)},
			message: "name must be at most 79 bytes",
		},
		{
			name:    "control byte in name",
			request: &l3bpb.DeleteServiceRequest{Name: "vs\x7f"},
			message: "name must contain only printable bytes",
		},
		{name: "named service", request: &l3bpb.DeleteServiceRequest{Name: "vs0"}},
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

// Test_UpdateModuleConfigRequest_Validate verifies that the required config,
// config name, and destination filter messages are validated recursively.
func Test_UpdateModuleConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *l3bpb.UpdateModuleConfigRequest
		message string
	}{
		{name: "missing config", request: &l3bpb.UpdateModuleConfigRequest{}, message: "config is required"},
		{
			name: "empty config name",
			request: &l3bpb.UpdateModuleConfigRequest{
				Config: &l3bpb.ModuleConfig{},
			},
			message: "config: name is required",
		},
		{
			name: "nil protocol range at repeated index",
			request: &l3bpb.UpdateModuleConfigRequest{
				Config: &l3bpb.ModuleConfig{
					Name: "l3b0",
					DestinationFilterRules: []*l3bpb.DestinationFilterRule{{
						ProtoRanges: []*filterpb.ProtoRange{nil},
					}},
				},
			},
			message: "config: destination_filter_rules[0]: proto_ranges[0] is required",
		},
		{
			name: "named config",
			request: &l3bpb.UpdateModuleConfigRequest{
				Config: &l3bpb.ModuleConfig{Name: "l3b0"},
			},
		},
		{name: "nil request", request: nil, message: "config is required"},
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

// Test_UpdateRealServerStateRequest_Validate verifies that state updates use
// the complete service-name validation rather than only an empty check.
func Test_UpdateRealServerStateRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *l3bpb.UpdateRealServerStateRequest
		message string
	}{
		{name: "empty service", request: &l3bpb.UpdateRealServerStateRequest{}, message: "service is required"},
		{
			name:    "overlong service",
			request: &l3bpb.UpdateRealServerStateRequest{Service: strings.Repeat("a", 80)},
			message: "service must be at most 79 bytes",
		},
		{
			name:    "control byte in service",
			request: &l3bpb.UpdateRealServerStateRequest{Service: "vs\n"},
			message: "service must contain only printable bytes",
		},
		{name: "named service", request: &l3bpb.UpdateRealServerStateRequest{Service: "vs0"}},
		{name: "nil request", request: nil, message: "service is required"},
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

// Test_UpdateRealServerWeightRequest_Validate verifies that weight updates use
// the complete service-name validation rather than only an empty check.
func Test_UpdateRealServerWeightRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *l3bpb.UpdateRealServerWeightRequest
		message string
	}{
		{name: "empty service", request: &l3bpb.UpdateRealServerWeightRequest{}, message: "service is required"},
		{
			name:    "overlong service",
			request: &l3bpb.UpdateRealServerWeightRequest{Service: strings.Repeat("a", 80)},
			message: "service must be at most 79 bytes",
		},
		{
			name:    "control byte in service",
			request: &l3bpb.UpdateRealServerWeightRequest{Service: "vs\n"},
			message: "service must contain only printable bytes",
		},
		{name: "named service", request: &l3bpb.UpdateRealServerWeightRequest{Service: "vs0"}},
		{name: "nil request", request: nil, message: "service is required"},
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

// Test_ListSessionsRequest_Validate verifies that session queries reject every
// service name that cannot be represented by the backend registry.
func Test_ListSessionsRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *l3bpb.ListSessionsRequest
		message string
	}{
		{name: "empty service", request: &l3bpb.ListSessionsRequest{}, message: "service is required"},
		{
			name:    "overlong service",
			request: &l3bpb.ListSessionsRequest{Service: strings.Repeat("a", 80)},
			message: "service must be at most 79 bytes",
		},
		{name: "named service", request: &l3bpb.ListSessionsRequest{Service: "vs0"}},
		{name: "nil request", request: nil, message: "service is required"},
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

// Test_GetServiceRequest_Validate verifies that inspection uses the same name
// rules as service creation and deletion.
func Test_GetServiceRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *l3bpb.GetServiceRequest
		message string
	}{
		{name: "empty name", request: &l3bpb.GetServiceRequest{}, message: "name is required"},
		{
			name:    "overlong name",
			request: &l3bpb.GetServiceRequest{Name: strings.Repeat("a", 80)},
			message: "name must be at most 79 bytes",
		},
		{name: "named service", request: &l3bpb.GetServiceRequest{Name: "vs0"}},
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

// Test_SourceFilterRule_Validate verifies that nil IPNet and port range
// elements receive their repeated-field paths instead of reaching conversion.
func Test_SourceFilterRule_Validate(t *testing.T) {
	cases := []struct {
		name    string
		rule    *l3bpb.SourceFilterRule
		message string
	}{
		{
			name:    "nil IPv6 network",
			rule:    &l3bpb.SourceFilterRule{Net6S: []*filterpb.IPNet{nil}},
			message: "net6s[0] is required",
		},
		{
			name:    "nil IPv4 network",
			rule:    &l3bpb.SourceFilterRule{Net4S: []*filterpb.IPNet{nil}},
			message: "net4s[0] is required",
		},
		{
			name:    "nil port range",
			rule:    &l3bpb.SourceFilterRule{PortRanges: []*filterpb.PortRange{nil}},
			message: "port_ranges[0] is required",
		},
		{
			name: "non-nil ranges",
			rule: &l3bpb.SourceFilterRule{
				Net6S:      []*filterpb.IPNet{{}},
				Net4S:      []*filterpb.IPNet{{}},
				PortRanges: []*filterpb.PortRange{{}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rule.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_DestinationFilterRule_Validate verifies that nil IPNet and protocol
// range elements receive their repeated-field paths before conversion.
func Test_DestinationFilterRule_Validate(t *testing.T) {
	cases := []struct {
		name    string
		rule    *l3bpb.DestinationFilterRule
		message string
	}{
		{
			name:    "nil IPv6 network",
			rule:    &l3bpb.DestinationFilterRule{Net6S: []*filterpb.IPNet{nil}},
			message: "net6s[0] is required",
		},
		{
			name:    "nil IPv4 network",
			rule:    &l3bpb.DestinationFilterRule{Net4S: []*filterpb.IPNet{nil}},
			message: "net4s[0] is required",
		},
		{
			name:    "nil protocol range",
			rule:    &l3bpb.DestinationFilterRule{ProtoRanges: []*filterpb.ProtoRange{nil}},
			message: "proto_ranges[0] is required",
		},
		{
			name: "non-nil ranges",
			rule: &l3bpb.DestinationFilterRule{
				Net6S:       []*filterpb.IPNet{{}},
				Net4S:       []*filterpb.IPNet{{}},
				ProtoRanges: []*filterpb.ProtoRange{{}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rule.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_VirtualService_Validate verifies that nested source filter errors keep
// their repeated-field path.
func Test_VirtualService_Validate(t *testing.T) {
	cases := []struct {
		name    string
		service *l3bpb.VirtualService
		message string
	}{
		{name: "empty name", service: &l3bpb.VirtualService{}, message: "name is required"},
		{
			name: "nil nested network",
			service: &l3bpb.VirtualService{
				Name: "vs0",
				SourceFilterRules: []*l3bpb.SourceFilterRule{{
					Net4S: []*filterpb.IPNet{nil},
				}},
			},
			message: "source_filter_rules[0]: net4s[0] is required",
		},
		{name: "named service", service: &l3bpb.VirtualService{Name: "vs0"}},
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

// Test_ModuleConfig_Validate verifies that nested destination filter errors
// keep their repeated-field path.
func Test_ModuleConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		config  *l3bpb.ModuleConfig
		message string
	}{
		{name: "empty name", config: &l3bpb.ModuleConfig{}, message: "name is required"},
		{
			name: "nil nested network",
			config: &l3bpb.ModuleConfig{
				Name: "l3b0",
				DestinationFilterRules: []*l3bpb.DestinationFilterRule{{
					Net6S: []*filterpb.IPNet{nil},
				}},
			},
			message: "destination_filter_rules[0]: net6s[0] is required",
		},
		{name: "named config", config: &l3bpb.ModuleConfig{Name: "l3b0"}},
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
