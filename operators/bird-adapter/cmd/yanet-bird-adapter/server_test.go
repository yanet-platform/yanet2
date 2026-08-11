package main

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Test_ShippedDefaultConfig_MatchesServerConfig guards against drift between
// the shipped default config file and the ServerConfig struct.
//
// With strict parsing enabled a stale or renamed key in the shipped YAML
// would break every fresh install, so this test loads the conffile through
// xcfg.WithKnownFields() and requires it to decode to exactly
// DefaultServerConfig(). That also catches the Go defaults silently
// drifting away from the shipped conffile.
func Test_ShippedDefaultConfig_MatchesServerConfig(t *testing.T) {
	cfg, err := xcfg.LoadConfig[ServerConfig]("../../etc/yanet/bird-adapter-default.yaml", xcfg.WithKnownFields())
	require.NoError(t, err)
	require.Equal(t, DefaultServerConfig(), cfg)
}

// Test_BIRDConfig_Validate asserts that an empty name disables the startup
// import regardless of the other fields, and that a filled-in name rejects
// a half-filled or wrong-family config.
func Test_BIRDConfig_Validate(t *testing.T) {
	validSockets := []string{"/var/run/bird/yanet-master4.sock"}
	validV4 := netip.MustParseAddr("127.0.0.1")
	validV6 := netip.MustParseAddr("::1")

	tests := []struct {
		name    string
		cfg     BIRDConfig
		wantErr bool
	}{
		{
			name:    "disabled",
			cfg:     BIRDConfig{},
			wantErr: false,
		},
		{
			name: "empty name disables import despite sockets and sources set",
			cfg: BIRDConfig{
				Sockets:  validSockets,
				SourceV4: validV4,
				SourceV6: validV6,
			},
			wantErr: false,
		},
		{
			name: "missing sockets",
			cfg: BIRDConfig{
				Name:     "route0",
				SourceV4: validV4,
				SourceV6: validV6,
			},
			wantErr: true,
		},
		{
			name: "missing source addresses",
			cfg: BIRDConfig{
				Name:    "route0",
				Sockets: validSockets,
			},
			wantErr: true,
		},
		{
			name: "ipv6 address in source_v4",
			cfg: BIRDConfig{
				Name:     "route0",
				Sockets:  validSockets,
				SourceV4: validV6,
				SourceV6: validV6,
			},
			wantErr: true,
		},
		{
			name: "ipv4-mapped address in source_v6",
			cfg: BIRDConfig{
				Name:     "route0",
				Sockets:  validSockets,
				SourceV4: validV4,
				SourceV6: netip.MustParseAddr("::ffff:127.0.0.1"),
			},
			wantErr: true,
		},
		{
			name: "fully valid",
			cfg: BIRDConfig{
				Name:     "route0",
				Sockets:  validSockets,
				SourceV4: validV4,
				SourceV6: validV6,
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
