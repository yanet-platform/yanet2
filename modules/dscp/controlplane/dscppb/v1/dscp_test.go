package dscppb_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xproto"
	dscppb "github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

// Test_Config_UnmarshalJSON_DestinationAliases verifies that legacy YAML
// destination keys and their explicit names decode to the same configuration.
func Test_Config_UnmarshalJSON_DestinationAliases(t *testing.T) {
	prefix4, err := commonpb.NewIPv4PrefixFromPrefix(netip.MustParsePrefix("192.0.2.0/24"))
	require.NoError(t, err)
	prefix6, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix("2001:db8::/32"))
	require.NoError(t, err)
	want := &dscppb.Config{
		DestinationPrefixes4: []*commonpb.IPv4Prefix{prefix4},
		DestinationPrefixes6: []*commonpb.IPv6Prefix{prefix6},
		DscpConfig:           &dscppb.DscpConfig{Flag: 2, Mark: 46},
	}
	cases := []struct {
		name     string
		prefixes string
	}{
		{
			name:     "legacy names",
			prefixes: "  prefixes4: [192.0.2.0/24]\n  prefixes6: ['2001:db8::/32']\n",
		},
		{
			name:     "explicit destination names",
			prefixes: "  destination_prefixes4: [192.0.2.0/24]\n  destination_prefixes6: ['2001:db8::/32']\n",
		},
		{
			name:     "legacy IPv4 and explicit IPv6",
			prefixes: "  prefixes4: [192.0.2.0/24]\n  destination_prefixes6: ['2001:db8::/32']\n",
		},
		{
			name:     "explicit IPv4 and legacy IPv6",
			prefixes: "  destination_prefixes4: [192.0.2.0/24]\n  prefixes6: ['2001:db8::/32']\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := &dscppb.UpdateConfigRequest{}
			require.NoError(t, xproto.Unmarshal([]byte(
				"name: mark\nconfig:\n"+tc.prefixes+"  dscp_config: {flag: 2, mark: 46}\n",
			), request))
			require.True(t, proto.Equal(want, request.GetConfig()),
				"want %v, got %v", want, request.GetConfig(),
			)
		})
	}
}

// Test_Config_UnmarshalJSON_RejectsAmbiguousAliases verifies that naming a
// destination set twice fails even when one spelling carries an empty value.
func Test_Config_UnmarshalJSON_RejectsAmbiguousAliases(t *testing.T) {
	cases := []struct {
		name     string
		document string
		message  string
	}{
		{
			name:     "IPv4 destination specified twice",
			document: "prefixes4: [192.0.2.0/24]\ndestination_prefixes4: [198.51.100.0/24]\n",
			message:  `both "prefixes4" and "destination_prefixes4" are specified`,
		},
		{
			name:     "IPv6 destination specified twice",
			document: "prefixes6: ['2001:db8::/32']\ndestination_prefixes6: ['2001:db8:1::/48']\n",
			message:  `both "prefixes6" and "destination_prefixes6" are specified`,
		},
		{
			name:     "empty explicit destination beside legacy destination",
			document: "prefixes4: [192.0.2.0/24]\ndestination_prefixes4: []\n",
			message:  `both "prefixes4" and "destination_prefixes4" are specified`,
		},
		{
			name:     "null legacy destination beside explicit destination",
			document: "prefixes6: null\ndestination_prefixes6: ['2001:db8::/32']\n",
			message:  `both "prefixes6" and "destination_prefixes6" are specified`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := xproto.Unmarshal([]byte(tc.document), &dscppb.Config{})
			require.ErrorContains(t, err, tc.message)
		})
	}
}

// Test_Config_UnmarshalJSON_ValidatesLegacyValues verifies that old destination
// keys retain value validation instead of being silently ignored.
func Test_Config_UnmarshalJSON_ValidatesLegacyValues(t *testing.T) {
	cases := []struct {
		name     string
		document string
	}{
		{"IPv4 alias with an IPv6 prefix", "prefixes4: ['2001:db8::/32']"},
		{"IPv6 alias with an IPv4 prefix", "prefixes6: [192.0.2.0/24]"},
		{"IPv4 alias with a scalar", "prefixes4: 17"},
		{"IPv6 alias with a null entry", "prefixes6: [null]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := xproto.Unmarshal([]byte(tc.document), &dscppb.Config{})
			require.Error(t, err)
		})
	}
}

// Test_ShowConfigRequest_Validate verifies that an empty name is rejected.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *dscppb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &dscppb.ShowConfigRequest{}, message: "name is required"},
		{name: "name set", request: &dscppb.ShowConfigRequest{Name: "dscp0"}},
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

// Test_UpdateConfigRequest_Validate verifies that the name and the config
// are required and that an out-of-range marking is reported at its field
// path.
func Test_UpdateConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *dscppb.UpdateConfigRequest
		message string
	}{
		{
			name:    "empty name precedes missing config",
			request: &dscppb.UpdateConfigRequest{},
			message: "name is required",
		},
		{
			name:    "missing config",
			request: &dscppb.UpdateConfigRequest{Name: "dscp0"},
			message: "config is required",
		},
		{
			name: "marking out of range",
			request: &dscppb.UpdateConfigRequest{
				Name:   "dscp0",
				Config: &dscppb.Config{DscpConfig: &dscppb.DscpConfig{Flag: 3}},
			},
			message: "config: dscp_config: flag 3 must be in range 0..2",
		},
		{
			name:    "config without a marking",
			request: &dscppb.UpdateConfigRequest{Name: "dscp0", Config: &dscppb.Config{}},
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

// Test_Config_Validate verifies that a set marking is range-checked at its
// field path while an unset one is accepted.
func Test_Config_Validate(t *testing.T) {
	cases := []struct {
		name    string
		config  *dscppb.Config
		message string
	}{
		{name: "no marking", config: &dscppb.Config{}},
		{
			name:   "marking at limit",
			config: &dscppb.Config{DscpConfig: &dscppb.DscpConfig{Flag: 2, Mark: 63}},
		},
		{
			name:    "mark above limit",
			config:  &dscppb.Config{DscpConfig: &dscppb.DscpConfig{Mark: 64}},
			message: "dscp_config: mark 64 must be in range 0..63",
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

// Test_DeleteConfigRequest_Validate verifies that an empty name is rejected.
func Test_DeleteConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *dscppb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &dscppb.DeleteConfigRequest{}, message: "name is required"},
		{name: "name set", request: &dscppb.DeleteConfigRequest{Name: "dscp0"}},
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

// Test_DscpConfig_Validate verifies that flag and mark are each rejected
// once they exceed their valid range.
func Test_DscpConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		config  *dscppb.DscpConfig
		message string
	}{
		{name: "zero values", config: &dscppb.DscpConfig{}},
		{name: "flag at limit", config: &dscppb.DscpConfig{Flag: 2}},
		{name: "mark at limit", config: &dscppb.DscpConfig{Mark: 63}},
		{
			name:    "flag above limit",
			config:  &dscppb.DscpConfig{Flag: 3},
			message: "flag 3 must be in range 0..2",
		},
		{
			name:    "mark above limit",
			config:  &dscppb.DscpConfig{Mark: 64},
			message: "mark 64 must be in range 0..63",
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
