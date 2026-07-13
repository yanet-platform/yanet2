package main

import (
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
