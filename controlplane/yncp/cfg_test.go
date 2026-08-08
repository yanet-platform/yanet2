package yncp_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/yncp"
)

// Test_ShippedDefaultConfig_NoUnknownKeys guards the shipped default config
// against a key that matches no field in yncp.Config.
//
// Config.UnmarshalYAML re-decodes through a fresh, non-strict yaml.v3
// decoder, so xcfg.WithKnownFields cannot see a stray key here.
// xcfg.CheckKnownKeys walks the reflected struct shape directly instead,
// which is unaffected by that re-decode.
func Test_ShippedDefaultConfig_NoUnknownKeys(t *testing.T) {
	data, err := os.ReadFile("../etc/yanet/controlplane.d/default.yaml")
	require.NoError(t, err)
	require.NoError(t, xcfg.CheckKnownKeys[yncp.Config](data))
}
